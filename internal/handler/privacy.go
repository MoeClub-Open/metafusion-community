package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/MoeclubM/metafusion-community/internal/account"
	"github.com/MoeclubM/metafusion-community/internal/auth"
)

// favoritesVisible 在读取用户收藏及计数前应用唯一权威偏好。只给本人绕过公开设置，
// 管理权限在独立管理 API 生效，不让公共端点对管理员浏览器返回可误缓存的私有列表。
// ok=false 表示响应已写入；未配置、故障或契约缺字段均拒绝公开。
func (h *Handler) favoritesVisible(c *gin.Context, ownerID string) (visible, ok bool) {
	if p := h.principal(c); p != nil && !p.IsThirdParty {
		owner, ownerErr := uuid.Parse(ownerID)
		viewer, viewerErr := uuid.Parse(p.ID)
		if ownerErr == nil && viewerErr == nil && owner == viewer {
			return true, true
		}
	}
	if h.account == nil {
		fail(c, http.StatusServiceUnavailable, auth.CodeAuthUnavailable)
		return false, false
	}
	// 只传 Gin 经既有受信代理策略解析出的单个来源 IP，不复制原始转发链。
	// 账号服务仍照常按真实访客限流，所有社区请求不会挤在同一个容器 IP 桶中。
	ctx := account.WithClientIP(c.Request.Context(), c.ClientIP())
	visible, err := h.account.FavoritesPublic(ctx, ownerID)
	if errors.Is(err, account.ErrNotFound) {
		fail(c, http.StatusNotFound, "not_found")
		return false, false
	}
	if err != nil {
		fail(c, http.StatusServiceUnavailable, auth.CodeAuthUnavailable)
		return false, false
	}
	return visible, true
}

// 当前没有向第三方 OAuth 授予收藏访问的 scope，不能把 profile 身份等同于本站本人授权。
// PAT 仍按现有个人令牌权限读取/操作收藏，不属于 IsThirdParty。
func (h *Handler) rejectThirdPartyFavorites(c *gin.Context) bool {
	if p := h.principal(c); p != nil && p.IsThirdParty {
		c.Header("Cache-Control", "no-store")
		fail(c, http.StatusForbidden, "forbidden")
		return true
	}
	return false
}
