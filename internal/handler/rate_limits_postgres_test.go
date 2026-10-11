package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/MoeclubM/metafusion-community/internal/catalog"
	"github.com/MoeclubM/metafusion-community/internal/store"
	"github.com/MoeclubM/metafusion-community/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestRuntimeRateLimitsAndMessagesAcrossRoutersAgainstPostgres(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	dsn := testutil.DSN(t)
	first, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	if err = first.Init(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	original, err := first.RateLimitConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policyJSON, _ := json.Marshal(original.Policy)
	messagesJSON, _ := json.Marshal(original.Messages)
	subject, peer1, peer2 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, err := first.DB().Exec(`UPDATE community.rate_limit_policy SET etag=$1,policy=$2::jsonb,messages=$3::jsonb,updated_at=$4,updated_by=NULLIF($5,'')::uuid WHERE singleton=true`, original.ETag, string(policyJSON), string(messagesJSON), original.UpdatedAt, original.UpdatedBy)
		if err != nil {
			t.Error(err)
		}
		for _, query := range []string{
			`DELETE FROM community.rate_limit_windows WHERE subject_key='user:'||$1`,
			`DELETE FROM community.message_rate_limit_buckets WHERE subject_key IN ('sender:'||$1,'stranger:'||$1)`,
			`DELETE FROM community.direct_messages WHERE sender_id=$1::uuid`,
			`DELETE FROM community.direct_message_settings WHERE user_id=$1::uuid`,
			`DELETE FROM audit.audit_log WHERE actor_user_id=$1::uuid`,
		} {
			if _, err := first.DB().Exec(query, subject); err != nil {
				t.Error(err)
			}
		}
	})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, kid := jwksServer(t, key)
	verifier := newVerifier(t, srv.URL)
	adminToken := signTokenWith(t, key, kid, subject, "admin", []string{"admin"}, []string{auth.PermissionRateLimitsManage})
	memberToken := signTokenWith(t, key, kid, uuid.NewString(), "user", []string{"member"}, []string{auth.PermissionPostCreate})
	newRouter := func(s *store.Store) *gin.Engine {
		r := gin.New()
		h := New(s, catalog.New("", 0), verifier)
		t.Cleanup(h.Close)
		h.Register(r)
		return r
	}
	r1, r2 := newRouter(first), newRouter(second)
	call := func(r *gin.Engine, method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {memberToken, 403}, {adminToken, 200}} {
		w := call(r1, http.MethodGet, "/api/community/admin/rate-limits", "", tc.token)
		if w.Code != tc.status {
			t.Fatalf("management permission: %d / %s", w.Code, w.Body.String())
		}
	}
	w := call(r1, http.MethodPut, "/api/community/admin/rate-limits", `{"policy":{}}`, adminToken)
	if w.Code != 400 {
		t.Fatalf("missing messages settings accepted: %d %s", w.Code, w.Body.String())
	}
	policy := store.RateLimitPolicy{DefaultUnlimited: true, Groups: map[string]store.RateLimitRule{"admin": {PerMinute: 2}}}
	messages := store.MessageRateLimitPolicy{SendPerMinute: 5, StrangerPerHour: 1, AdminUnlimited: false}
	saveBody, _ := json.Marshal(map[string]any{"expected_etag": original.ETag, "policy": policy, "messages": messages, "edit_note": "limit admin during test"})
	w = call(r1, http.MethodPut, "/api/community/admin/rate-limits", string(saveBody), adminToken)
	var saved store.RateLimitConfig
	if err := json.Unmarshal(w.Body.Bytes(), &saved); w.Code != 200 || err != nil || saved.ETag == original.ETag {
		t.Fatalf("management save: %d / %s / %v", w.Code, w.Body.String(), err)
	}
	// Two processes share one admin read+write budget. Rejected writes must not execute.
	w = call(r1, http.MethodGet, "/api/community/admin/rate-limits", "", adminToken)
	if w.Code != 200 || w.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Fatalf("first budget: %d / %s", w.Code, w.Body.String())
	}
	w = call(r2, http.MethodPut, "/api/messages/settings", `{"accept_from_strangers":false}`, adminToken)
	if w.Code != 200 || w.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("shared write budget: %d / %s", w.Code, w.Body.String())
	}
	w = call(r1, http.MethodGet, "/api/community/admin/rate-limits", "", adminToken)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("finite admin exemption not revoked: %d / %s", w.Code, w.Body.String())
	}
	policy.Groups = nil // default_unlimited applies, leaving the message anti-harassment quota observable.
	saved, err = second.SaveRateLimitConfig(ctx, saved.ETag, policy, messages, subject)
	if err != nil {
		t.Fatal(err)
	}
	w = call(r1, http.MethodPost, "/api/messages/with/"+peer1, `{"body":"hello"}`, adminToken)
	if w.Code != 200 {
		t.Fatalf("first stranger: %d / %s", w.Code, w.Body.String())
	}
	w = call(r2, http.MethodPost, "/api/messages/with/"+peer2, `{"body":"hello"}`, adminToken)
	if w.Code != 429 {
		t.Fatalf("stranger quota not shared: %d / %s", w.Code, w.Body.String())
	}
	w = call(r2, http.MethodPost, "/api/messages/with/"+peer1, `{"body":"continue"}`, adminToken)
	if w.Code != 200 {
		t.Fatalf("existing conversation charged stranger quota: %d / %s", w.Code, w.Body.String())
	}
	messages.AdminUnlimited = true
	if _, err = first.SaveRateLimitConfig(ctx, saved.ETag, policy, messages, subject); err != nil {
		t.Fatal(err)
	}
	w = call(r2, http.MethodPost, "/api/messages/with/"+peer2, `{"body":"admin exemption restored"}`, adminToken)
	if w.Code != 200 {
		t.Fatalf("dynamic admin message exemption: %d / %s", w.Code, w.Body.String())
	}
	w = call(r2, http.MethodPut, "/api/community/admin/rate-limits", string(saveBody), adminToken)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "conflict") {
		t.Fatalf("stale management save: %d / %s", w.Code, w.Body.String())
	}
}
