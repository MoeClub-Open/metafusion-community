package handler

import (
	"net/http"
	"testing"

	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/google/uuid"
)

func TestBoardCreationPermissionsAndDuplicateCode(t *testing.T) {
	ctx, db, router, key, kid := opsFixture(t)
	payload := `{"code":"new-board","names":{"zh-CN":"新板块","zh-TW":"新板塊","en-US":"New board","ja-JP":"新しい板"}}`
	member := signTokenWith(t, key, kid, uuid.NewString(), "user", nil, []string{auth.PermissionPostCreate})
	manager := signTokenWith(t, key, kid, uuid.NewString(), "user", nil, []string{auth.PermissionBoardManage})
	for _, tc := range []struct {
		bearer string
		status int
	}{{"", 401}, {member, 403}, {manager, 201}, {manager, 409}} {
		response := opsCall(t, router, http.MethodPost, "/api/community/boards", payload, tc.bearer)
		if response.Code != tc.status {
			t.Fatalf("want %d got %d: %s", tc.status, response.Code, response.Body.String())
		}
	}
	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM community.boards WHERE code='new-board' AND names->>'en-US'='New board' AND descriptions='{}'::jsonb AND is_enabled AND show_in_feed`).Scan(&total); err != nil || total != 1 {
		t.Fatalf("created board: %d %v", total, err)
	}
	for _, invalid := range []string{`{"code":"bad/code","names":{"en-US":"Name"}}`, `{"code":"other-board","names":{"en-US":"Name"}}`} {
		response := opsCall(t, router, http.MethodPost, "/api/community/boards", invalid, manager)
		if response.Code != 400 {
			t.Fatalf("invalid board accepted: %d %s", response.Code, response.Body.String())
		}
	}
}
