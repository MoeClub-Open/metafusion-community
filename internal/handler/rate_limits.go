package handler

import (
	"errors"
	"strings"

	"github.com/MoeclubM/metafusion-community/internal/audit"
	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/MoeclubM/metafusion-community/internal/store"
	"github.com/gin-gonic/gin"
)

func (h *Handler) registerRateLimits(api *gin.RouterGroup) {
	limits := api.Group("/community/admin/rate-limits", h.require(auth.PermissionRateLimitsManage))
	limits.GET("", func(c *gin.Context) {
		cfg, err := h.store.RateLimitConfig(c.Request.Context())
		if err != nil {
			fail(c, 503, "rate_limit_unavailable")
			return
		}
		c.JSON(200, cfg)
	})
	limits.PUT("", func(c *gin.Context) {
		var in struct {
			Policy       *store.RateLimitPolicy        `json:"policy"`
			Messages     *store.MessageRateLimitPolicy `json:"messages"`
			ExpectedETag string                        `json:"expected_etag"`
			EditNote     string                        `json:"edit_note"`
		}
		if !body(c, &in) {
			return
		}
		if in.Policy == nil || in.Messages == nil || len(strings.TrimSpace(in.EditNote)) > 4000 {
			fail(c, 400, "invalid_rate_limit")
			return
		}
		cfg, err := h.store.SaveRateLimitConfig(c.Request.Context(), in.ExpectedETag, *in.Policy, *in.Messages, h.principal(c).ID)
		if err != nil {
			if errors.Is(err, store.ErrInvalidRateLimit) {
				fail(c, 400, "invalid_rate_limit")
			} else if errors.Is(err, store.ErrRateLimitConflict) {
				fail(c, 409, "conflict")
			} else {
				fail(c, 503, "rate_limit_unavailable")
			}
			return
		}
		audit.Describe(c, audit.Detail{TargetType: "rate_limits", TargetID: "community-rate-limits", Changes: map[string]any{
			"default_per_minute": cfg.Policy.DefaultPerMinute, "default_unlimited": cfg.Policy.DefaultUnlimited,
			"groups": len(cfg.Policy.Groups), "accounts": len(cfg.Policy.Accounts),
			"messages": cfg.Messages, "edit_note": strings.TrimSpace(in.EditNote),
		}})
		c.JSON(200, cfg)
	})
}
