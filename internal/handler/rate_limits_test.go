package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/MoeclubM/metafusion-community/internal/store"
	"github.com/gin-gonic/gin"
)

func TestMessageAdminExemptionCanBeRevoked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/messages/with/peer", nil)
	h := &Handler{store: &store.Store{}, messages: newMessageLimiter()}
	h.messages.now = func() time.Time { return time.Unix(1700000000, 0) }
	cfg := store.DefaultRateLimitConfig()
	cfg.Messages.SendPerMinute, cfg.Messages.StrangerPerHour = 1, 1
	c.Set(rateLimitConfigKey, cfg)
	admin := &auth.Principal{ID: "admin", Groups: []string{"admin"}}
	for i := 0; i < 3; i++ {
		if ok, _, err := h.allowMessage(c, admin, false); err != nil || !ok {
			t.Fatalf("default exemption: %v / %v", ok, err)
		}
	}
	cfg.Messages.AdminUnlimited = false
	c.Set(rateLimitConfigKey, cfg)
	if ok, _, err := h.allowMessage(c, admin, false); err != nil || !ok {
		t.Fatalf("first limited admin message: %v / %v", ok, err)
	}
	if ok, retry, err := h.allowMessage(c, admin, false); err != nil || ok || retry <= 0 {
		t.Fatalf("admin override not enforced: %v / %v / %v", ok, retry, err)
	}
	if ok, _, err := h.allowMessage(c, admin, true); err != nil || !ok {
		t.Fatalf("first limited admin stranger: %v / %v", ok, err)
	}
	if ok, retry, err := h.allowMessage(c, admin, true); err != nil || ok || retry <= 0 {
		t.Fatalf("admin stranger override not enforced: %v / %v / %v", ok, retry, err)
	}
	// Third-party group claims never gain the verified-admin messaging exemption.
	cfg.Messages.AdminUnlimited = true
	c.Set(rateLimitConfigKey, cfg)
	oauth := &auth.Principal{ID: "oauth", Groups: []string{"admin"}, IsThirdParty: true}
	if ok, _, err := h.allowMessage(c, oauth, false); err != nil || !ok {
		t.Fatalf("first oauth message: %v / %v", ok, err)
	}
	if ok, _, err := h.allowMessage(c, oauth, false); err != nil || ok {
		t.Fatalf("oauth gained admin exemption: %v / %v", ok, err)
	}
}
