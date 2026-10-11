package handler

import (
	"strconv"
	"sync"
	"time"

	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/MoeclubM/metafusion-community/internal/store"
	"github.com/gin-gonic/gin"
)

const rateLimitConfigKey = "community.rate_limit_config"

// Production uses community PostgreSQL for policy and atomic shared counters.
// The in-memory branch is only for handlers built without a database in unit tests.
func apiRateLimiter(stores ...*store.Store) gin.HandlerFunc {
	var s *store.Store
	if len(stores) > 0 {
		s = stores[0]
	}
	type window struct {
		start time.Time
		used  int
	}
	var mu sync.Mutex
	buckets := map[string]window{}
	return func(c *gin.Context) {
		cfg, err := s.RateLimitConfig(c.Request.Context())
		if err != nil {
			c.Abort()
			fail(c, 503, "rate_limit_unavailable")
			return
		}
		c.Set(rateLimitConfigKey, cfg)
		key := "ip:" + c.ClientIP()
		var identities, groups []string
		p := auth.Current(c)
		if p != nil {
			key = "user:" + p.ID
			identities = []string{p.ID, p.Username}
			groups = p.Groups
			if p.IsThirdParty {
				// Untrusted OAuth group claims cannot inherit the default admin exemption.
				groups = make([]string, 0, len(p.Groups))
				for _, group := range p.Groups {
					if group != "admin" {
						groups = append(groups, group)
					}
				}
			}
		}
		limit, unlimited := cfg.Policy.Resolve(identities, groups, p == nil)
		if unlimited {
			c.Next()
			return
		}
		used, reset := 0, 0
		if s != nil && s.DB() != nil {
			used, reset, err = s.ConsumeAPIRateLimit(c.Request.Context(), key)
		} else {
			now := time.Now()
			mu.Lock()
			if len(buckets) >= 65536 {
				for k, v := range buckets {
					if now.Sub(v.start) >= time.Minute {
						delete(buckets, k)
					}
				}
			}
			b := buckets[key]
			if now.Sub(b.start) >= time.Minute {
				b = window{start: now}
			}
			b.used++
			buckets[key] = b
			mu.Unlock()
			used = b.used
			reset = max(1, int(time.Until(b.start.Add(time.Minute)).Seconds())+1)
		}
		if err != nil {
			c.Abort()
			fail(c, 503, "rate_limit_unavailable")
			return
		}
		c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(max(0, limit-used)))
		c.Header("X-RateLimit-Reset", strconv.Itoa(reset))
		if used > limit {
			c.Header("Retry-After", strconv.Itoa(reset))
			c.Abort()
			fail(c, 429, "rate_limited")
			return
		}
		c.Next()
	}
}

func verifiedAdmin(p *auth.Principal) bool {
	return p != nil && !p.IsThirdParty && contains(p.Groups, "admin")
}

func (h *Handler) allowMessage(c *gin.Context, p *auth.Principal, stranger bool) (bool, time.Duration, error) {
	value, exists := c.Get(rateLimitConfigKey)
	cfg, ok := value.(store.RateLimitConfig)
	if !exists || !ok {
		var err error
		cfg, err = h.store.RateLimitConfig(c.Request.Context())
		if err != nil {
			return false, 0, err
		}
	}
	if cfg.Messages.AdminUnlimited && verifiedAdmin(p) {
		return true, 0, nil
	}
	capacity, window, key := cfg.Messages.SendPerMinute, time.Minute, "sender:"+p.ID
	if stranger {
		capacity, window, key = cfg.Messages.StrangerPerHour, time.Hour, "stranger:"+p.ID
	}
	if h.db != nil {
		return h.store.ConsumeMessageRateLimit(c.Request.Context(), key, capacity, window)
	}
	h.messages.mu.Lock()
	defer h.messages.mu.Unlock()
	bucket := &h.messages.general
	if stranger {
		bucket = &h.messages.stranger
	}
	bucket.capacity, bucket.window = capacity, window
	allowed, retry := bucket.allow(h.messages.now(), p.ID)
	return allowed, retry, nil
}
