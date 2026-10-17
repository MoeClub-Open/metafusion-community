package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// 用户互动统计由本服务自有表提供。收藏数量与公开列表共用账号隐私判定：
// 对非本人且未公开的收藏返回 null，而不是伪造为零；主题与回复数字仍正常展示。
func (h *Handler) registerStats(api *gin.RouterGroup) {
	api.GET("/users/:id/stats", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		ownerID := c.Param("id")
		if _, err := uuid.Parse(ownerID); err != nil {
			fail(c, 404, "not_found")
			return
		}
		visible, ok := h.favoritesVisible(c, ownerID)
		if !ok {
			return
		}
		stats, err := h.store.StatsFor(c.Request.Context(), ownerID, commentBoard)
		if err != nil {
			fail(c, 500, "module_error")
			return
		}
		var favoritesCount *int
		if visible {
			favoritesCount = &stats.FavoritesCount
		}
		c.JSON(200, gin.H{"stats": gin.H{
			"topics_created":   stats.TopicsCreated,
			"comments_created": stats.CommentsCreated,
			"favorites_count":  favoritesCount,
		}})
	})
}
