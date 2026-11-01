package handler

import (
	"database/sql"
	"regexp"
	"strings"

	"github.com/MoeclubM/metafusion-community/internal/audit"
	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/gin-gonic/gin"
)

var boardCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func (h *Handler) registerBoardCreation(api *gin.RouterGroup) {
	api.POST("/community/boards", h.require(auth.PermissionBoardManage), func(c *gin.Context) {
		var in struct {
			Code         string            `json:"code"`
			Names        map[string]string `json:"names"`
			Descriptions map[string]string `json:"descriptions"`
			Color        string            `json:"color"`
			Icon         string            `json:"icon"`
			SortOrder    int               `json:"sort_order"`
			IsEnabled    *bool             `json:"is_enabled"`
			ShowInFeed   *bool             `json:"show_in_feed"`
		}
		if !body(c, &in) {
			return
		}
		in.Code = strings.TrimSpace(in.Code)
		if !boardCodePattern.MatchString(in.Code) {
			fail(c, 400, "invalid_board_code")
			return
		}
		names, code := resolveBoardLocales(in.Names)
		if code != "" {
			fail(c, 400, code)
			return
		}
		if len(names) == 0 {
			fail(c, 400, "invalid_payload")
			return
		}
		descriptions := map[string]string{}
		if in.Descriptions != nil {
			descriptions, code = resolveBoardLocales(in.Descriptions)
			if code != "" {
				fail(c, 400, code)
				return
			}
		}
		namesJSON, _ := encodeLocales(names)
		descriptionsJSON, _ := encodeLocales(descriptions)
		color, icon := strings.TrimSpace(in.Color), strings.TrimSpace(in.Icon)
		if color == "" {
			color = "emerald"
		}
		if icon == "" {
			icon = "BookOpen"
		}
		enabled, feed := in.IsEnabled == nil || *in.IsEnabled, in.ShowInFeed == nil || *in.ShowInFeed
		row := h.db.QueryRowContext(c.Request.Context(), `INSERT INTO community.boards(code,names,descriptions,color,icon,sort_order,is_enabled,show_in_feed)
		 VALUES($1,$2::jsonb,$3::jsonb,$4,$5,$6,$7,$8) ON CONFLICT(code) DO NOTHING RETURNING `+boardCols,
			in.Code, namesJSON, descriptionsJSON, color, icon, in.SortOrder, enabled, feed)
		board, err := scanBoardRow(row.Scan)
		if err == sql.ErrNoRows {
			fail(c, 409, "board_exists")
			return
		}
		if err != nil {
			fail(c, 500, "module_error")
			return
		}
		audit.Describe(c, audit.Detail{TargetType: "board", TargetID: board.Code, Changes: map[string]any{"names": board.Names, "is_enabled": board.IsEnabled, "show_in_feed": board.ShowInFeed}})
		c.JSON(201, board)
	})
}
