package store

import (
	"errors"
	"testing"
)

func TestRateLimitPolicyOverridesAndAdminRule(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	if _, unlimited := cfg.Policy.Resolve([]string{"u1", "name"}, []string{"admin"}, false); !unlimited {
		t.Fatal("seeded admin rule must default to unlimited")
	}
	cfg.Policy.Groups["admin"] = RateLimitRule{PerMinute: 12}
	if limit, unlimited := cfg.Policy.Resolve([]string{"u1", "name"}, []string{"admin"}, false); limit != 12 || unlimited {
		t.Fatalf("explicit admin quota not applied: %d / %v", limit, unlimited)
	}
	cfg.Policy.Accounts = map[string]RateLimitRule{"u1": {PerMinute: 3}, "name": {Unlimited: true}}
	if limit, unlimited := cfg.Policy.Resolve([]string{"u1", "name"}, []string{"admin"}, false); limit != 3 || unlimited {
		t.Fatalf("account ID must override username and group: %d / %v", limit, unlimited)
	}
	delete(cfg.Policy.Accounts, "u1")
	if _, unlimited := cfg.Policy.Resolve([]string{"u1", "name"}, []string{"admin"}, false); !unlimited {
		t.Fatal("username override missing")
	}
	cfg.Policy.Groups["editor"] = RateLimitRule{PerMinute: 30}
	if limit, unlimited := cfg.Policy.Resolve(nil, []string{"admin", "editor"}, false); limit != 30 || unlimited {
		t.Fatal("multiple group grants must use the most permissive rule")
	}
	delete(cfg.Policy.Groups, "admin")
	if limit, unlimited := cfg.Policy.Resolve(nil, []string{"admin"}, false); limit != 180 || unlimited {
		t.Fatal("removing admin rule must revoke exemption")
	}
	cfg.Policy.Groups["anonymous"] = RateLimitRule{PerMinute: 2}
	if limit, unlimited := cfg.Policy.Resolve([]string{"name"}, []string{"admin"}, true); limit != 2 || unlimited {
		t.Fatal("anonymous must only use the reserved anonymous rule")
	}
	if limit, unlimited := (RateLimitPolicy{}).Resolve(nil, nil, false); limit != 180 || unlimited {
		t.Fatal("empty policy must use route default")
	}
}

func TestRateLimitConfigRejectsInvalidBudgets(t *testing.T) {
	for _, mutate := range []func(*RateLimitConfig){
		func(c *RateLimitConfig) { c.Messages.SendPerMinute = 0 },
		func(c *RateLimitConfig) { c.Messages.StrangerPerHour = -1 },
		func(c *RateLimitConfig) { c.Policy.DefaultPerMinute = maxRateLimit + 1 },
		func(c *RateLimitConfig) { c.Policy.Groups[" admin"] = RateLimitRule{PerMinute: 1} },
		func(c *RateLimitConfig) { c.Policy.Accounts = map[string]RateLimitRule{"x": {PerMinute: -1}} },
	} {
		cfg := DefaultRateLimitConfig()
		mutate(&cfg)
		if err := ValidateRateLimitConfig(cfg.Policy, cfg.Messages); !errors.Is(err, ErrInvalidRateLimit) {
			t.Fatalf("invalid settings accepted: %+v / %v", cfg, err)
		}
	}
}
