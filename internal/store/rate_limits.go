package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const DefaultRateLimitPerMinute = 180
const maxRateLimit = 1000000

var ErrInvalidRateLimit = errors.New("invalid_rate_limit")
var ErrRateLimitConflict = errors.New("conflict")

type RateLimitRule struct {
	PerMinute int  `json:"per_minute,omitempty"`
	Unlimited bool `json:"unlimited,omitempty"`
}

type RateLimitPolicy struct {
	DefaultPerMinute int                      `json:"default_per_minute,omitempty"`
	DefaultUnlimited bool                     `json:"default_unlimited,omitempty"`
	Groups           map[string]RateLimitRule `json:"groups,omitempty"`
	Accounts         map[string]RateLimitRule `json:"accounts,omitempty"`
}

type MessageRateLimitPolicy struct {
	SendPerMinute   int  `json:"send_per_minute"`
	StrangerPerHour int  `json:"stranger_per_hour"`
	AdminUnlimited  bool `json:"admin_unlimited"`
}

type RateLimitConfig struct {
	ETag      string                 `json:"etag"`
	Policy    RateLimitPolicy        `json:"policy"`
	Messages  MessageRateLimitPolicy `json:"messages"`
	UpdatedAt time.Time              `json:"updated_at"`
	UpdatedBy string                 `json:"updated_by,omitempty"`
}

func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Policy: RateLimitPolicy{DefaultPerMinute: DefaultRateLimitPerMinute,
			Groups: map[string]RateLimitRule{"admin": {Unlimited: true}}},
		Messages: MessageRateLimitPolicy{SendPerMinute: 180, StrangerPerHour: 5, AdminUnlimited: true},
	}
}

// Resolve applies account overrides first, then the most permissive matching group.
// Admin has no special branch: its default exemption is an editable seeded group rule.
func (p RateLimitPolicy) Resolve(identities, groups []string, anonymous bool) (int, bool) {
	if anonymous {
		groups = []string{"anonymous"}
	} else {
		for _, identity := range identities {
			if identity == "" {
				continue
			}
			if r, ok := p.Accounts[identity]; ok && (r.Unlimited || r.PerMinute > 0) {
				return r.PerMinute, r.Unlimited
			}
		}
	}
	best := 0
	for _, group := range groups {
		if r, ok := p.Groups[group]; ok {
			if r.Unlimited {
				return 0, true
			}
			best = max(best, r.PerMinute)
		}
	}
	if best > 0 {
		return best, false
	}
	if p.DefaultUnlimited {
		return 0, true
	}
	if p.DefaultPerMinute > 0 {
		return p.DefaultPerMinute, false
	}
	return DefaultRateLimitPerMinute, false
}

func ValidateRateLimitConfig(policy RateLimitPolicy, messages MessageRateLimitPolicy) error {
	if policy.DefaultPerMinute < 0 || policy.DefaultPerMinute > maxRateLimit ||
		len(policy.Groups) > 200 || len(policy.Accounts) > 2000 ||
		messages.SendPerMinute <= 0 || messages.SendPerMinute > maxRateLimit ||
		messages.StrangerPerHour <= 0 || messages.StrangerPerHour > maxRateLimit {
		return ErrInvalidRateLimit
	}
	for _, rules := range []map[string]RateLimitRule{policy.Groups, policy.Accounts} {
		for key, rule := range rules {
			if key == "" || strings.TrimSpace(key) != key || len(key) > 200 ||
				rule.PerMinute < 0 || rule.PerMinute > maxRateLimit {
				return ErrInvalidRateLimit
			}
		}
	}
	return nil
}

func (s *Store) RateLimitConfig(ctx context.Context) (RateLimitConfig, error) {
	if s == nil || s.db == nil {
		return DefaultRateLimitConfig(), nil
	}
	var cfg RateLimitConfig
	var policy, messages []byte
	err := s.db.QueryRowContext(ctx, `SELECT etag,policy,messages,updated_at,COALESCE(updated_by::text,'')
		FROM community.rate_limit_policy WHERE singleton=true`).Scan(&cfg.ETag, &policy, &messages, &cfg.UpdatedAt, &cfg.UpdatedBy)
	if err != nil {
		return cfg, err
	}
	if err = json.Unmarshal(policy, &cfg.Policy); err != nil {
		return cfg, err
	}
	if err = json.Unmarshal(messages, &cfg.Messages); err != nil {
		return cfg, err
	}
	if err = ValidateRateLimitConfig(cfg.Policy, cfg.Messages); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *Store) SaveRateLimitConfig(ctx context.Context, expectedETag string, policy RateLimitPolicy, messages MessageRateLimitPolicy, actorID string) (RateLimitConfig, error) {
	if err := ValidateRateLimitConfig(policy, messages); err != nil {
		return RateLimitConfig{}, err
	}
	if expectedETag == "" {
		return RateLimitConfig{}, ErrRateLimitConflict
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return RateLimitConfig{}, err
	}
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return RateLimitConfig{}, err
	}
	var cfg RateLimitConfig
	cfg.Policy, cfg.Messages, cfg.UpdatedBy = policy, messages, actorID
	err = s.db.QueryRowContext(ctx, `UPDATE community.rate_limit_policy
		SET etag=$1,policy=$2::jsonb,messages=$3::jsonb,updated_at=now(),updated_by=$4::uuid
		WHERE singleton=true AND etag=$5 RETURNING etag,updated_at`,
		uuid.NewString(), string(policyJSON), string(messagesJSON), actorID, expectedETag).Scan(&cfg.ETag, &cfg.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, ErrRateLimitConflict
	}
	return cfg, err
}

// ConsumeAPIRateLimit uses the database clock and one atomic UPSERT for all replicas.
// Read and write requests intentionally use the same subject key.
func (s *Store) ConsumeAPIRateLimit(ctx context.Context, key string) (used, retryAfter int, err error) {
	s.pruneRateLimitBuckets(ctx)
	err = s.db.QueryRowContext(ctx, `WITH clock AS (SELECT clock_timestamp() AS ts), attempt AS (
		INSERT INTO community.rate_limit_windows AS w(subject_key,window_start,used,expires_at)
		SELECT $1,date_trunc('minute',ts),1,ts+interval '2 minutes' FROM clock
		ON CONFLICT(subject_key) DO UPDATE SET
		window_start=GREATEST(w.window_start,EXCLUDED.window_start),
		used=CASE WHEN w.window_start>=EXCLUDED.window_start THEN LEAST(w.used+1,1000001) ELSE 1 END,
		expires_at=GREATEST(w.expires_at,EXCLUDED.expires_at) RETURNING window_start,used
	) SELECT used,LEAST(60,GREATEST(1,CEIL(EXTRACT(epoch FROM window_start+interval '1 minute'-clock_timestamp()))))::integer
	FROM attempt`, key).Scan(&used, &retryAfter)
	return
}

// ConsumeMessageRateLimit preserves the continuous-refill token bucket semantics.
// Persisted capacity/window let a policy update refill at the previous rate up to
// the new capacity, rather than awarding a full bucket when an operator saves.
func (s *Store) ConsumeMessageRateLimit(ctx context.Context, key string, capacity int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	if capacity <= 0 || capacity > maxRateLimit || window < time.Second {
		return false, 0, ErrInvalidRateLimit
	}
	s.pruneRateLimitBuckets(ctx)
	refill := `LEAST(EXCLUDED.capacity::double precision,b.tokens+GREATEST(0,EXTRACT(epoch FROM EXCLUDED.updated_at-b.updated_at))*b.capacity/b.window_seconds)`
	query := fmt.Sprintf(`INSERT INTO community.message_rate_limit_buckets AS b
		(subject_key,tokens,capacity,window_seconds,updated_at,expires_at,allowed,retry_after)
		VALUES($1,$2-1,$2,$3,clock_timestamp(),clock_timestamp()+make_interval(secs=>GREATEST(600,$3)*2),true,0)
		ON CONFLICT(subject_key) DO UPDATE SET
		tokens=CASE WHEN %[1]s>=1 THEN %[1]s-1 ELSE %[1]s END,
		allowed=%[1]s>=1,
		retry_after=CASE WHEN %[1]s>=1 THEN 0 ELSE GREATEST(1,CEIL((1-%[1]s)*EXCLUDED.window_seconds/EXCLUDED.capacity))::integer END,
		capacity=EXCLUDED.capacity,window_seconds=EXCLUDED.window_seconds,
		updated_at=GREATEST(b.updated_at,EXCLUDED.updated_at),expires_at=GREATEST(b.expires_at,EXCLUDED.expires_at)
		RETURNING allowed,retry_after`, refill)
	var seconds int
	err = s.db.QueryRowContext(ctx, query, key, capacity, int(window.Seconds())).Scan(&allowed, &seconds)
	return allowed, time.Duration(seconds) * time.Second, err
}

// Idle counters expire in bounded batches. Cleanup failure does not grant quota;
// the authoritative consume operation still reports its database error.
// Keep expiry in the outer DELETE predicate so a concurrent renewal is rechecked
// after PostgreSQL waits for its row lock, rather than deleting the stale key.
func (s *Store) pruneRateLimitBuckets(ctx context.Context) {
	now := time.Now().Unix()
	previous := s.rateLimitCleanupAt.Load()
	if now-previous < 60 || !s.rateLimitCleanupAt.CompareAndSwap(previous, now) {
		return
	}
	for _, table := range []string{"rate_limit_windows", "message_rate_limit_buckets"} {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM community.`+table+`
			WHERE expires_at<now() AND subject_key IN
			(SELECT subject_key FROM community.`+table+` WHERE expires_at<now() ORDER BY expires_at LIMIT 1000)`)
	}
}
