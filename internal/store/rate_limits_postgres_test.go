package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MoeclubM/metafusion-community/internal/testutil"
	"github.com/google/uuid"
)

func TestRateLimitCountersSharedBetweenInstancesAgainstPostgres(t *testing.T) {
	ctx := context.Background()
	dsn := testutil.DSN(t)
	first, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	if err = first.Init(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	key := "test:" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = first.db.Exec(`DELETE FROM community.rate_limit_windows WHERE subject_key=$1`, key)
		_, _ = first.db.Exec(`DELETE FROM community.message_rate_limit_buckets WHERE subject_key=$1`, key)
	})
	var startMinute, endMinute time.Time
	if err = first.db.QueryRow(`SELECT date_trunc('minute',clock_timestamp())`).Scan(&startMinute); err != nil {
		t.Fatal(err)
	}
	const requests, limit = 24, 5
	results := make(chan int, requests)
	errorsCh := make(chan error, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := first
			if i%2 == 1 {
				s = second
			}
			used, retry, err := s.ConsumeAPIRateLimit(ctx, key)
			if err != nil {
				errorsCh <- err
				return
			}
			if retry < 1 || retry > 60 {
				errorsCh <- fmt.Errorf("invalid retry %d", retry)
				return
			}
			results <- used
		}(i)
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	if err = first.db.QueryRow(`SELECT date_trunc('minute',clock_timestamp())`).Scan(&endMinute); err != nil {
		t.Fatal(err)
	}
	if startMinute.Equal(endMinute) {
		seen, allowed := map[int]bool{}, 0
		for used := range results {
			if seen[used] {
				t.Fatalf("two instances returned the same counter: %d", used)
			}
			seen[used] = true
			if used <= limit {
				allowed++
			}
		}
		if len(seen) != requests || allowed != limit {
			t.Fatalf("shared quota: counters=%d allowed=%d", len(seen), allowed)
		}
	} else {
		t.Log("API requests crossed the documented minute reset; atomic token buckets checked below")
	}
	for i := 0; i < limit+2; i++ {
		s := first
		if i%2 == 1 {
			s = second
		}
		allowed, retry, err := s.ConsumeMessageRateLimit(ctx, key, limit, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if allowed != (i < limit) {
			t.Fatalf("shared message bucket attempt %d: allowed=%v", i, allowed)
		}
		if !allowed && retry <= 0 {
			t.Fatal("denial must carry a retry")
		}
	}
	// Database-clock refill remains available after the first process is replaced.
	if _, err = first.db.Exec(`UPDATE community.message_rate_limit_buckets SET updated_at=clock_timestamp()-interval '1 hour' WHERE subject_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if allowed, _, err := second.ConsumeMessageRateLimit(ctx, key, limit, time.Hour); err != nil || !allowed {
		t.Fatalf("persistent refill after restart: %v / %v", allowed, err)
	}
	// Tightening a live bucket clamps its remaining tokens to the new capacity.
	if allowed, _, err := first.ConsumeMessageRateLimit(ctx, key, 1, time.Hour); err != nil || !allowed {
		t.Fatalf("tightened bucket: %v / %v", allowed, err)
	}
	if allowed, _, err := second.ConsumeMessageRateLimit(ctx, key, 1, time.Hour); err != nil || allowed {
		t.Fatalf("tightening awarded extra tokens: %v / %v", allowed, err)
	}
}

// A request can capture its minute before waiting for the row lock. Another
// instance may have already advanced the stored minute when that request runs.
// Preload a newer minute to exercise the late-request branch deterministically.
func TestRateLimitWindowCannotMoveBackwardsAgainstPostgres(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testutil.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	key := "late-request:" + uuid.NewString()
	t.Cleanup(func() {
		if _, err := s.db.Exec(`DELETE FROM community.rate_limit_windows WHERE subject_key=$1`, key); err != nil {
			t.Error(err)
		}
	})
	var newerMinute, expiry time.Time
	err = s.db.QueryRow(`INSERT INTO community.rate_limit_windows(subject_key,window_start,used,expires_at)
		VALUES($1,date_trunc('minute',clock_timestamp())+interval '2 minutes',3,clock_timestamp()+interval '4 minutes')
		RETURNING window_start,expires_at`, key).Scan(&newerMinute, &expiry)
	if err != nil {
		t.Fatal(err)
	}
	used, retry, err := s.ConsumeAPIRateLimit(ctx, key)
	if err != nil || used != 4 || retry < 1 || retry > 60 {
		t.Fatalf("late request reset the existing quota: used=%d retry=%d err=%v", used, retry, err)
	}
	var actualMinute, actualExpiry time.Time
	if err = s.db.QueryRow(`SELECT window_start,expires_at FROM community.rate_limit_windows WHERE subject_key=$1`, key).Scan(&actualMinute, &actualExpiry); err != nil {
		t.Fatal(err)
	}
	if !actualMinute.Equal(newerMinute) || !actualExpiry.Equal(expiry) {
		t.Fatalf("late request moved persisted time backwards: minute=%v expiry=%v", actualMinute, actualExpiry)
	}
}

func TestRateLimitCleanupPreservesConcurrentRefreshAgainstPostgres(t *testing.T) {
	s, err := Open(context.Background(), testutil.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"rate_limit_windows", "message_rate_limit_buckets"} {
		t.Run(table, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			key := "cleanup-race:" + uuid.NewString()
			t.Cleanup(func() {
				if _, err := s.db.Exec(`DELETE FROM community.`+table+` WHERE subject_key=$1`, key); err != nil {
					t.Error(err)
				}
			})
			insert := `INSERT INTO community.rate_limit_windows(subject_key,window_start,used,expires_at)
				VALUES($1,now()-interval '3 minutes',180,now()-interval '1 minute')`
			refreshSQL := `UPDATE community.rate_limit_windows SET
				window_start=date_trunc('minute',clock_timestamp()),expires_at=clock_timestamp()+interval '2 minutes' WHERE subject_key=$1`
			if table == "message_rate_limit_buckets" {
				insert = `INSERT INTO community.message_rate_limit_buckets
					(subject_key,tokens,capacity,window_seconds,updated_at,expires_at,allowed,retry_after)
					VALUES($1,0,180,60,now()-interval '3 hours',now()-interval '1 minute',false,1)`
				refreshSQL = `UPDATE community.message_rate_limit_buckets SET
					updated_at=clock_timestamp(),expires_at=clock_timestamp()+interval '2 hours' WHERE subject_key=$1`
			}
			if _, err := s.db.ExecContext(ctx, insert, key); err != nil {
				t.Fatal(err)
			}
			refresh, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer refresh.Rollback()
			if _, err := refresh.ExecContext(ctx, refreshSQL, key); err != nil {
				t.Fatal(err)
			}
			// Cleanup sees the expired committed version and waits for this renewal.
			// The outer expiry predicate must be rechecked after the renewal commits.
			cleaner := &Store{db: s.db}
			finished := make(chan struct{})
			go func() { cleaner.pruneRateLimitBuckets(ctx); close(finished) }()
			deadline := time.Now().Add(3 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
					SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
					AND query LIKE $1 AND wait_event='transactionid'
				)`, "DELETE FROM community."+table+"%").Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case <-finished:
					t.Fatal("cleanup completed before competing renewal committed")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if !blocked {
				t.Fatal("cleanup never waited on the renewal row")
			}
			if err := refresh.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			valueColumn := "used"
			want := 180
			if table == "message_rate_limit_buckets" {
				valueColumn, want = "tokens", 0
			}
			var value int
			var active bool
			if err := s.db.QueryRowContext(ctx, `SELECT `+valueColumn+`,expires_at>now() FROM community.`+table+` WHERE subject_key=$1`, key).Scan(&value, &active); err != nil || value != want || !active {
				t.Fatalf("cleanup erased or reset renewed quota: value=%d active=%v err=%v", value, active, err)
			}
		})
	}
}

func TestRateLimitPolicyCASAgainstPostgres(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testutil.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	original, err := s.RateLimitConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policyJSON, _ := json.Marshal(original.Policy)
	messagesJSON, _ := json.Marshal(original.Messages)
	t.Cleanup(func() {
		_, err := s.db.Exec(`UPDATE community.rate_limit_policy SET etag=$1,policy=$2::jsonb,messages=$3::jsonb,updated_at=$4,updated_by=NULLIF($5,'')::uuid WHERE singleton=true`, original.ETag, string(policyJSON), string(messagesJSON), original.UpdatedAt, original.UpdatedBy)
		if err != nil {
			t.Error(err)
		}
	})
	policy := DefaultRateLimitConfig().Policy
	policy.Groups["admin"] = RateLimitRule{PerMinute: 7}
	messages := MessageRateLimitPolicy{SendPerMinute: 20, StrangerPerHour: 3, AdminUnlimited: false}
	saved, err := s.SaveRateLimitConfig(ctx, original.ETag, policy, messages, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if saved.ETag == original.ETag {
		t.Fatal("successful save did not change etag")
	}
	if _, err = s.SaveRateLimitConfig(ctx, original.ETag, policy, messages, uuid.NewString()); !errors.Is(err, ErrRateLimitConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	current, err := s.RateLimitConfig(ctx)
	if err != nil || current.Messages.AdminUnlimited || current.Messages.SendPerMinute != 20 {
		t.Fatalf("dynamic message config: %+v / %v", current, err)
	}
	if limit, unlimited := current.Policy.Resolve(nil, []string{"admin"}, false); limit != 7 || unlimited {
		t.Fatalf("persisted admin override: %d / %v", limit, unlimited)
	}
}
