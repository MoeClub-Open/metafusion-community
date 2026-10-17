package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/MoeclubM/metafusion-community/internal/account"
	"github.com/MoeclubM/metafusion-community/internal/auth"
)

// 真库验证隐私切换同时约束收藏列表及计数，且本人和独立治理读不受影响。
func TestFavoritesPrivacyAgainstPostgres(t *testing.T) {
	ownerID := uuid.NewString()
	var public atomic.Bool
	var status, requests atomic.Int64
	status.Store(200)
	accountStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/tokens/introspect" {
			_ = json.NewEncoder(w).Encode(gin.H{"valid": true, "user_id": ownerID, "username": "owner", "groups": []string{}, "permissions": []string{}})
			return
		}
		requests.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("viewer credentials reached public profile")
		}
		w.WriteHeader(int(status.Load()))
		_ = json.NewEncoder(w).Encode(gin.H{"user": gin.H{"id": ownerID, "favorites_public": public.Load()}})
	}))
	t.Cleanup(accountStub.Close)
	ctx, db, router, key, kid := opsFixture(t, func(h *Handler) {
		h.SetAccountClient(account.New(accountStub.URL))
		h.verifier.SetPAT(auth.NewPATIntrospector(accountStub.URL))
	})
	t.Cleanup(func() { cleanupStats(ctx, db, ownerID) })
	for i := 0; i < 2; i++ {
		exec(t, ctx, db, `INSERT INTO community.favorites(user_id,target_type,target_id) VALUES($1,'work',$2)`, ownerID, uuid.NewString())
	}
	ownerToken := signTokenWith(t, key, kid, ownerID, "user", nil, nil)
	visitorToken := signTokenWith(t, key, kid, uuid.NewString(), "user", nil, nil)
	adminToken := signTokenWith(t, key, kid, uuid.NewString(), "user", nil, []string{auth.PermissionReportReview})
	oauthToken := signFavoritesOAuth(t, key, kid, ownerID)
	patToken := auth.PATPrefix + strings.Repeat("x", 43)
	listPath := "/api/users/" + ownerID + "/favorites"
	statsPath := "/api/users/" + ownerID + "/stats"
	for _, token := range []string{"", visitorToken, adminToken, oauthToken} {
		assertHiddenFavorites(t, opsCall(t, router, http.MethodGet, listPath, "", token))
		w := opsCall(t, router, http.MethodGet, statsPath, "", token)
		var got struct {
			Stats map[string]any `json:"stats"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Stats["favorites_count"] != nil {
			t.Fatalf("private count leaked: %d %s", w.Code, w.Body.String())
		}
		if _, exists := got.Stats["favorites_count"]; !exists || got.Stats["topics_created"] != float64(0) || got.Stats["comments_created"] != float64(0) {
			t.Fatalf("hidden count must be explicit null and preserve public stats: %s", w.Body.String())
		}
	}
	assertVisible := func(path, token string, countOnly bool) {
		t.Helper()
		w := opsCall(t, router, http.MethodGet, path, "", token)
		var got struct {
			Total   int  `json:"total"`
			Visible bool `json:"visible"`
			Stats   struct {
				FavoritesCount *int `json:"favorites_count"`
			} `json:"stats"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("visible response: %d %s", w.Code, w.Body.String())
		}
		if countOnly {
			if got.Stats.FavoritesCount == nil || *got.Stats.FavoritesCount != 2 {
				t.Fatalf("owner/public count: %s", w.Body.String())
			}
		} else if got.Total != 2 || !got.Visible {
			t.Fatalf("owner/public favorites: %s", w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("cache headers: %v", w.Header())
		}
	}
	beforeOwn := requests.Load()
	assertVisible(listPath, ownerToken, false)
	assertVisible("/api/users/"+strings.ToUpper(ownerID)+"/favorites", ownerToken, false)
	assertVisible("/api/favorites/mine", ownerToken, false)
	assertVisible(statsPath, ownerToken, true)
	if requests.Load() != beforeOwn {
		t.Fatal("owner visibility depended on public account lookup")
	}
	// 个人 PAT 保持本人能力，profile-only OAuth 即使 sub 相同也不获得它。
	assertVisible(listPath, patToken, false)
	assertVisible("/api/favorites/mine", patToken, false)
	assertVisible(statsPath, patToken, true)
	for _, path := range []string{"/api/favorites/mine", "/api/favorites/status"} {
		w := opsCall(t, router, http.MethodGet, path, "", oauthToken)
		if w.Code != 403 || opsErrorCode(t, w) != "forbidden" {
			t.Fatalf("OAuth self read: %d %s", w.Code, w.Body.String())
		}
	}
	// 管理原始记录的入口独立授权，不受公开偏好和目录解析影响。
	w := opsCall(t, router, http.MethodGet, adminFavoritesPath+"?owner_id="+ownerID, "", adminToken)
	var inventory struct {
		Total int `json:"total"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &inventory) != nil || inventory.Total != 2 {
		t.Fatalf("admin inventory: %d %s", w.Code, w.Body.String())
	}

	public.Store(true)
	assertVisible(listPath, "", false)
	assertVisible(statsPath, "", true)
	public.Store(false)
	assertHiddenFavorites(t, opsCall(t, router, http.MethodGet, listPath, "", ""))
	// 上游故障/缺失用户均明确报错，不能变成公开列表；自己的读取继续可用。
	for _, code := range []int{503, 404} {
		status.Store(int64(code))
		for _, path := range []string{listPath, statsPath} {
			w = opsCall(t, router, http.MethodGet, path, "", "")
			want := auth.CodeAuthUnavailable
			if code == 404 {
				want = "not_found"
			}
			if w.Code != code || opsErrorCode(t, w) != want {
				t.Fatalf("upstream failure: %d %s", w.Code, w.Body.String())
			}
		}
		assertVisible(listPath, ownerToken, false)
		assertVisible(statsPath, ownerToken, true)
	}
}
