package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/MoeclubM/metafusion-community/internal/audit"
	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/MoeclubM/metafusion-community/internal/store"
)

func (h *Handler) registerAdminFavorites(api *gin.RouterGroup) {
	api.GET("/community/admin/favorites", h.require(auth.PermissionReportReview), func(c *gin.Context) {
		filter, ok := adminFavoriteFilter(c, false)
		if !ok {
			return
		}
		limit, offset := pagingPageSize(c, 20)
		items, total, err := h.store.ListAdminFavorites(c.Request.Context(), filter, limit, offset)
		if err != nil {
			fail(c, 500, "module_error")
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
	})
	api.DELETE("/community/admin/favorites", h.require(auth.PermissionReportReview), func(c *gin.Context) {
		filter, ok := adminFavoriteFilter(c, true)
		if !ok {
			return
		}
		changes := map[string]any{"owner_id": filter.OwnerID, "target_type": filter.TargetType, "target_id": filter.TargetID}
		audit.Describe(c, audit.Detail{TargetType: "favorite", TargetID: filter.TargetID, Changes: changes})
		deleted, err := h.store.DeleteAdminFavorite(c.Request.Context(), filter.OwnerID, filter.TargetType, filter.TargetID)
		if errors.Is(err, sql.ErrNoRows) {
			fail(c, http.StatusNotFound, "not_found")
			return
		}
		if err != nil {
			fail(c, 500, "module_error")
			return
		}
		audit.Describe(c, audit.Detail{Changes: map[string]any{"created_at": deleted.CreatedAt}})
		c.Status(http.StatusNoContent)
	})
}

// 管理员按库内原始键操作，包括历史别名或不可见目标，不调用目录服务。
func adminFavoriteFilter(c *gin.Context, required bool) (store.AdminFavoriteFilter, bool) {
	filter := store.AdminFavoriteFilter{TargetType: strings.TrimSpace(c.Query("target_type"))}
	for _, part := range []struct {
		query string
		value *string
	}{
		{"owner_id", &filter.OwnerID},
		{"target_id", &filter.TargetID},
	} {
		value := strings.TrimSpace(c.Query(part.query))
		if value == "" && !required {
			continue
		}
		id, err := uuid.Parse(value)
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid_"+part.query)
			return store.AdminFavoriteFilter{}, false
		}
		*part.value = id.String()
	}
	if filter.TargetType != "" || required {
		if _, err := store.KindFor(filter.TargetType); err != nil {
			fail(c, http.StatusBadRequest, "invalid_target_type")
			return store.AdminFavoriteFilter{}, false
		}
	}
	return filter, true
}
