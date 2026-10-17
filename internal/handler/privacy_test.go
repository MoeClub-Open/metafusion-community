package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/MoeclubM/metafusion-community/internal/account"
	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/MoeclubM/metafusion-community/internal/catalog"
	"github.com/MoeclubM/metafusion-community/internal/store"
)

func signFavoritesOAuth(t *testing.T, key *rsa.PrivateKey, kid, ownerID string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": ownerID, "preferred_username": "external-viewer", "role": "admin",
		"iss": testIssuer, "aud": testAudience, "exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(), "jti": uuid.NewString(), "token_use": "oauth",
		"client_id": "external-profile-client", "scope": "openid profile",
		"permissions": []string{auth.PermissionReportReview},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func withPublicAccountProfiles(t *testing.T) func(*Handler) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/users/")
		_ = json.NewEncoder(w).Encode(gin.H{"user": gin.H{"id": id, "favorites_public": true}})
	}))
	t.Cleanup(srv.Close)
	return func(h *Handler) { h.SetAccountClient(account.New(srv.URL)) }
}

// 没有账号资料也不能默认公开；所有判定均在触碰数据库或目录之前完成。
func TestFavoritesPrivacyErrorsWithoutDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := jwksServer(t, key)
	ownerID := uuid.NewString()
	for _, tc := range []struct {
		name, response, code string
		status, want         int
	}{
		{"missing user", `{"error":"not_found"}`, "not_found", 404, 404},
		{"dependency down", `{}`, auth.CodeAuthUnavailable, 503, 503},
		{"missing preference", `{"user":{"id":"` + ownerID + `"}}`, auth.CodeAuthUnavailable, 200, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer srv.Close()
			h := New(&store.Store{}, catalog.New("", 0), newVerifier(t, jwks.URL))
			h.SetAccountClient(account.New(srv.URL))
			router := gin.New()
			h.Register(router)
			for _, suffix := range []string{"favorites", "stats"} {
				w := opsCall(t, router, http.MethodGet, "/api/users/"+ownerID+"/"+suffix, "", "")
				if w.Code != tc.want || opsErrorCode(t, w) != tc.code || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("%s: %d %s headers=%v", suffix, w.Code, w.Body.String(), w.Header())
				}
			}
		})
	}
	// 未注入账号客户端也按不可用处理，不能读库并泄露收藏数量。
	router := gin.New()
	New(&store.Store{}, catalog.New("", 0), newVerifier(t, jwks.URL)).Register(router)
	for _, suffix := range []string{"favorites", "stats"} {
		w := opsCall(t, router, http.MethodGet, "/api/users/"+ownerID+"/"+suffix, "", "")
		if w.Code != 503 || opsErrorCode(t, w) != auth.CodeAuthUnavailable {
			t.Fatalf("unconfigured %s: %d %s", suffix, w.Code, w.Body.String())
		}
	}
}

func TestPrivateFavoritesDoNotReadDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := jwksServer(t, key)
	ownerID := uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(gin.H{"user": gin.H{"id": ownerID, "favorites_public": false}})
	}))
	defer srv.Close()
	h := New(&store.Store{}, catalog.New("", 0), newVerifier(t, jwks.URL))
	h.SetAccountClient(account.New(srv.URL))
	router := gin.New()
	h.Register(router)
	w := opsCall(t, router, http.MethodGet, "/api/users/"+ownerID+"/favorites?page=999&target_type=work", "", "")
	assertHiddenFavorites(t, w)
}

func assertHiddenFavorites(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var got struct {
		Items   []json.RawMessage `json:"items"`
		Total   int               `json:"total"`
		Visible bool              `json:"visible"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Items == nil || len(got.Items) != 0 || got.Total != 0 || got.Visible {
		t.Fatalf("private favorites leaked: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("private response may be cached: %v", w.Header())
	}
}

func TestFavoritesPrivacyUsesOnlyTrustedClientIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := jwksServer(t, key)
	ownerID := uuid.NewString()
	for _, tc := range []struct{ name, remote, forwarded, want string }{
		{"untrusted spoof ignored", "192.0.2.11:5000", "198.51.100.99", "192.0.2.11"},
		{"trusted proxy resolved", "127.0.0.1:5000", "198.51.100.12, 127.0.0.2", "198.51.100.12"},
		{"missing forwarding context", "192.0.2.12:5000", "", "192.0.2.12"},
		{"invalid remote context", "invalid", "198.51.100.99", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-Forwarded-For"); got != tc.want {
					t.Errorf("forwarded IP=%q want %q", got, tc.want)
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("request credentials reached public account endpoint")
				}
				_ = json.NewEncoder(w).Encode(gin.H{"user": gin.H{"id": ownerID, "favorites_public": false}})
			}))
			defer srv.Close()
			h := New(&store.Store{}, catalog.New("", 0), newVerifier(t, jwks.URL))
			h.SetAccountClient(account.New(srv.URL))
			router := gin.New()
			if err := router.SetTrustedProxies([]string{"127.0.0.0/8"}); err != nil {
				t.Fatal(err)
			}
			h.Register(router)
			req := httptest.NewRequest(http.MethodGet, "/api/users/"+ownerID+"/favorites", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", tc.forwarded)
			req.Header.Set("Cookie", "unrelated=secret")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			assertHiddenFavorites(t, w)
		})
	}
}

func TestThirdPartyFavoritesCannotUseOwnerIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks, kid := jwksServer(t, key)
	ownerID := uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(gin.H{"user": gin.H{"id": ownerID, "favorites_public": false}})
	}))
	defer srv.Close()
	h := New(&store.Store{}, catalog.New("", 0), newVerifier(t, jwks.URL))
	h.SetAccountClient(account.New(srv.URL))
	router := gin.New()
	h.Register(router)
	oauth := signFavoritesOAuth(t, key, kid, ownerID)
	ownerSession := signTokenWith(t, key, kid, ownerID, "user", nil, nil)
	callWithOwnerCookie := func(method, path, payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+oauth)
		req.AddCookie(&http.Cookie{Name: "mf_session", Value: ownerSession})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/favorites/mine"},
		{http.MethodGet, "/api/favorites/status?target_type=work&target_ids=" + uuid.NewString()},
		{http.MethodPost, "/api/favorites/toggle"},
	} {
		w := callWithOwnerCookie(tc.method, tc.path, `{}`)
		if w.Code != 403 || opsErrorCode(t, w) != "forbidden" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("third-party self favorites: %s %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	assertHiddenFavorites(t, callWithOwnerCookie(http.MethodGet, "/api/users/"+ownerID+"/favorites", ""))
	w := opsCall(t, router, http.MethodGet, "/api/favorites/status?target_type=work", "", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"favorited":[]}` {
		t.Fatalf("anonymous favorite status: %d %s", w.Code, w.Body.String())
	}
}
