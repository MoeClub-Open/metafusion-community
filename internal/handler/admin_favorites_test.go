package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/MoeclubM/metafusion-community/internal/audit"
	"github.com/MoeclubM/metafusion-community/internal/auth"
	"github.com/MoeclubM/metafusion-community/internal/catalog"
	"github.com/MoeclubM/metafusion-community/internal/store"
	"github.com/MoeclubM/metafusion-community/internal/testutil"
)

const adminFavoritesPath = "/api/community/admin/favorites"

func adminFavoritesTokens(t *testing.T) (*auth.Verifier, string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, kid := jwksServer(t, key)
	member := signTokenWith(t, key, kid, uuid.NewString(), "user", []string{"member"}, nil)
	admin := signTokenWith(t, key, kid, uuid.NewString(), "user", []string{"admin"}, []string{auth.PermissionReportReview})
	// Even an OAuth token carrying a governance permission and admin group is rejected.
	claims := jwt.MapClaims{
		"sub": uuid.NewString(), "preferred_username": "third-party", "role": "admin",
		"iss": testIssuer, "aud": testAudience, "exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(), "jti": uuid.NewString(), "token_use": "oauth",
		"client_id": "external-client", "scope": "openid profile",
		"groups": []string{"admin"}, "permissions": []string{auth.PermissionReportReview},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	oauth, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return newVerifier(t, srv.URL), admin, member, oauth
}

func TestAdminFavoritesAuthorizationWithoutDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	verifier, _, member, oauth := adminFavoritesTokens(t)
	router := gin.New()
	New(&store.Store{}, catalog.New("", 0), verifier).Register(router)
	query := "?owner_id=" + uuid.NewString() + "&target_type=work&target_id=" + uuid.NewString()
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		for _, tc := range []struct {
			name, bearer, code string
			status             int
		}{
			{"anonymous", "", "authentication_required", http.StatusUnauthorized},
			{"without_permission", member, "forbidden", http.StatusForbidden},
			{"third_party", oauth, "forbidden", http.StatusForbidden},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				w := opsCall(t, router, method, adminFavoritesPath+query, "", tc.bearer)
				if w.Code != tc.status || opsErrorCode(t, w) != tc.code {
					t.Fatalf("want %d %s, got %d %s", tc.status, tc.code, w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestAdminFavoritesInvalidInputWithoutDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	verifier, admin, _, _ := adminFavoritesTokens(t)
	router := gin.New()
	New(&store.Store{}, catalog.New("", 0), verifier).Register(router)
	owner, target := uuid.NewString(), uuid.NewString()
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		cases := []struct{ query, code string }{
			{"owner_id=bad&target_type=work&target_id=" + target, "invalid_owner_id"},
			{"owner_id=" + owner + "&target_type=work&target_id=bad", "invalid_target_id"},
			{"owner_id=" + owner + "&target_type=unknown&target_id=" + target, "invalid_target_type"},
		}
		if method == http.MethodDelete {
			cases = append(cases,
				struct{ query, code string }{"target_type=work&target_id=" + target, "invalid_owner_id"},
				struct{ query, code string }{"owner_id=" + owner + "&target_type=work", "invalid_target_id"},
				struct{ query, code string }{"owner_id=" + owner + "&target_id=" + target, "invalid_target_type"},
			)
		}
		for _, tc := range cases {
			t.Run(method+"/"+tc.query, func(t *testing.T) {
				w := opsCall(t, router, method, adminFavoritesPath+"?"+tc.query, "", admin)
				if w.Code != http.StatusBadRequest || opsErrorCode(t, w) != tc.code {
					t.Fatalf("want 400 %s, got %d %s", tc.code, w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestAdminFavoriteFilterAcceptsOptionalAndExactKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, target := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		query    string
		required bool
		want     store.AdminFavoriteFilter
	}{
		{"", false, store.AdminFavoriteFilter{}},
		{"owner_id=" + owner, false, store.AdminFavoriteFilter{OwnerID: owner}},
		{"target_id=" + target, false, store.AdminFavoriteFilter{TargetID: target}},
		{"target_type=work", false, store.AdminFavoriteFilter{TargetType: "work"}},
		{"owner_id=" + strings.ToUpper(owner) + "&target_type=work&target_id=" + strings.ToUpper(target), true,
			store.AdminFavoriteFilter{OwnerID: owner, TargetType: "work", TargetID: target}},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, adminFavoritesPath+"?"+tc.query, nil)
		got, ok := adminFavoriteFilter(c, tc.required)
		if !ok || got != tc.want {
			t.Fatalf("filter %q: got %+v, valid=%v, want %+v", tc.query, got, ok, tc.want)
		}
	}
	for kind := range store.FavoriteKinds {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, adminFavoritesPath+"?target_type="+kind, nil)
		if got, ok := adminFavoriteFilter(c, false); !ok || got.TargetType != kind {
			t.Fatalf("supported kind %s was rejected: %+v", kind, got)
		}
	}
	if _, exists := auditActions["GET "+adminFavoritesPath]; exists {
		t.Fatal("read-only favorites listing must not emit a write audit event")
	}
	if got := auditActions["DELETE "+adminFavoritesPath]; got != "favorite.deleted" {
		t.Fatalf("delete audit action = %q", got)
	}
}

type adminFavoriteTestRow struct {
	OwnerID    string    `json:"owner_id"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type adminFavoriteTestPage struct {
	Items []adminFavoriteTestRow `json:"items"`
	Total int                    `json:"total"`
}

type adminFavoritesFixture struct {
	ctx          context.Context
	db           *sql.DB
	h            *Handler
	router       http.Handler
	admin        string
	member       string
	catalogCalls atomic.Int32
	owners       []string
	requestIDs   []string
}

// Isolate only randomly generated owners and request IDs; existing test data is preserved.
func newAdminFavoritesFixture(t *testing.T) *adminFavoritesFixture {
	t.Helper()
	dsn := testutil.DSN(t)
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	f := &adminFavoritesFixture{ctx: ctx, db: s.DB()}
	cat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.catalogCalls.Add(1)
		http.Error(w, "admin raw-record API must not query catalog", http.StatusInternalServerError)
	}))
	t.Cleanup(cat.Close)
	verifier, admin, member, _ := adminFavoritesTokens(t)
	f.admin, f.member = admin, member
	f.h = New(s, catalog.New(cat.URL, time.Second), verifier)
	router := gin.New()
	f.h.Register(router)
	f.router = router
	t.Cleanup(func() {
		f.h.Close()
		_, _ = f.db.ExecContext(ctx, "DELETE FROM community.favorites WHERE user_id = ANY($1::uuid[])", pq.Array(f.owners))
		_, _ = f.db.ExecContext(ctx, "DELETE FROM audit.audit_log WHERE request_id = ANY($1)", pq.Array(f.requestIDs))
	})
	return f
}

func (f *adminFavoritesFixture) call(t *testing.T, method, query, bearer string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rid := "admin-favorites-" + uuid.NewString()
	f.requestIDs = append(f.requestIDs, rid)
	req := httptest.NewRequest(method, adminFavoritesPath+query, nil)
	req.Header.Set("X-Request-Id", rid)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	flushCtx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	if err := f.h.audit.Flush(flushCtx); err != nil {
		t.Fatalf("flush audit: %v", err)
	}
	return w, rid
}

func (f *adminFavoritesFixture) list(t *testing.T, query string) adminFavoriteTestPage {
	t.Helper()
	w, rid := f.call(t, http.MethodGet, query, f.admin)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var out adminFavoriteTestPage
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Items == nil {
		t.Fatal("items must be an array, including for an empty result")
	}
	if rows := queryAuditRows(t, f.ctx, f.db, rid); len(rows) != 0 {
		t.Fatalf("GET produced %d audit records", len(rows))
	}
	return out
}

func favoriteAdminQuery(owner, kind, target string) string {
	return "?" + url.Values{"owner_id": {owner}, "target_type": {kind}, "target_id": {target}}.Encode()
}

func TestAdminFavoritesPaginationAndRawFiltersAgainstPostgres(t *testing.T) {
	f := newAdminFavoritesFixture(t)
	ownerA, ownerB := uuid.NewString(), uuid.NewString()
	f.owners = []string{ownerA, ownerB}
	target, other := uuid.NewString(), uuid.NewString()
	baseline := f.list(t, "?page=1&page_size=1").Total
	created := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, row := range []adminFavoriteTestRow{
		{OwnerID: ownerA, TargetType: "work", TargetID: target},
		{OwnerID: ownerB, TargetType: "work", TargetID: target},
		{OwnerID: ownerA, TargetType: "release", TargetID: target},
		{OwnerID: ownerA, TargetType: "work", TargetID: other},
	} {
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO community.favorites(user_id,target_type,target_id,created_at) VALUES($1,$2,$3,$4)`,
			row.OwnerID, row.TargetType, row.TargetID, created.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.list(t, "?page=1&page_size=1"); got.Total != baseline+4 || len(got.Items) != 1 {
		t.Fatalf("all-user listing = %+v, baseline=%d", got, baseline)
	}
	for _, tc := range []struct {
		query string
		count int
	}{
		{"?owner_id=" + ownerA, 3},
		{"?target_id=" + target, 3},
		{"?target_id=" + target + "&target_type=work", 2},
		{favoriteAdminQuery(ownerB, "work", target), 1},
		{favoriteAdminQuery(ownerB, "release", target), 0},
	} {
		got := f.list(t, tc.query)
		if got.Total != tc.count || len(got.Items) != tc.count {
			t.Fatalf("filter %s = %+v, want count %d", tc.query, got, tc.count)
		}
	}
	first := f.list(t, "?target_id="+target+"&page=1&page_size=1")
	second := f.list(t, "?target_id="+target+"&page=2&page_size=1")
	if first.Total != 3 || second.Total != 3 || len(first.Items) != 1 || len(second.Items) != 1 ||
		first.Items[0].TargetType != "release" || second.Items[0].OwnerID != ownerB ||
		!first.Items[0].CreatedAt.Equal(created.Add(2*time.Minute)) {
		t.Fatalf("page slicing or raw keys changed: first=%+v, second=%+v", first, second)
	}
	if got := f.list(t, "?target_id="+target+"&page=4&page_size=1"); got.Total != 3 || len(got.Items) != 0 {
		t.Fatalf("past last page = %+v", got)
	}
	if calls := f.catalogCalls.Load(); calls != 0 {
		t.Fatalf("raw admin listing called catalog %d times", calls)
	}
}

func TestAdminFavoritesExactDeleteAndAuditAgainstPostgres(t *testing.T) {
	f := newAdminFavoritesFixture(t)
	ownerA, ownerB := uuid.NewString(), uuid.NewString()
	f.owners = []string{ownerA, ownerB}
	target, alias := uuid.NewString(), uuid.NewString()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, row := range []adminFavoriteTestRow{
		{OwnerID: ownerA, TargetType: "work", TargetID: target},
		{OwnerID: ownerB, TargetType: "work", TargetID: target},
		{OwnerID: ownerA, TargetType: "release", TargetID: target},
		{OwnerID: ownerA, TargetType: "work", TargetID: alias},
	} {
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO community.favorites(user_id,target_type,target_id,created_at) VALUES($1,$2,$3,$4)`,
			row.OwnerID, row.TargetType, row.TargetID, created); err != nil {
			t.Fatal(err)
		}
	}
	query := favoriteAdminQuery(ownerA, "work", target)
	w, rid := f.call(t, http.MethodDelete, query, f.member)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member delete = %d %s", w.Code, w.Body.String())
	}
	denied := queryAuditRows(t, f.ctx, f.db, rid)
	if len(denied) != 1 || denied[0].Action != "favorite.deleted" || denied[0].Result != audit.ResultFailure || denied[0].ErrorCode != "forbidden" {
		t.Fatalf("denied delete audit = %+v", denied)
	}
	if got := f.list(t, query); got.Total != 1 {
		t.Fatal("denied delete removed the row")
	}
	w, rid = f.call(t, http.MethodDelete, query, f.admin)
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	rows := queryAuditRows(t, f.ctx, f.db, rid)
	if len(rows) != 1 {
		t.Fatalf("delete audit count = %d", len(rows))
	}
	row := rows[0]
	if row.Action != "favorite.deleted" || row.Result != audit.ResultSuccess || row.TargetType != "favorite" || row.TargetID != target || row.HTTPStatus != http.StatusNoContent {
		t.Fatalf("delete audit metadata = %+v", row)
	}
	var changes adminFavoriteTestRow
	if err := json.Unmarshal([]byte(row.Changes), &changes); err != nil {
		t.Fatal(err)
	}
	if changes.OwnerID != ownerA || changes.TargetType != "work" || changes.TargetID != target || !changes.CreatedAt.Equal(created) {
		t.Fatalf("delete must audit the original stored row: %+v", changes)
	}
	for _, remaining := range []struct{ owner, kind, target string }{
		{ownerB, "work", target}, {ownerA, "release", target}, {ownerA, "work", alias},
	} {
		if got := f.list(t, favoriteAdminQuery(remaining.owner, remaining.kind, remaining.target)); got.Total != 1 {
			t.Fatalf("delete changed a neighboring key: %+v", remaining)
		}
	}
	w, rid = f.call(t, http.MethodDelete, query, f.admin)
	if w.Code != http.StatusNotFound || opsErrorCode(t, w) != "not_found" {
		t.Fatalf("second delete = %d %s", w.Code, w.Body.String())
	}
	rows = queryAuditRows(t, f.ctx, f.db, rid)
	if len(rows) != 1 || rows[0].Action != "favorite.deleted" || rows[0].Result != audit.ResultFailure || rows[0].ErrorCode != "not_found" || rows[0].TargetType != "favorite" || rows[0].TargetID != target {
		t.Fatalf("missing row delete audit = %+v", rows)
	}
	if calls := f.catalogCalls.Load(); calls != 0 {
		t.Fatalf("raw admin delete called catalog %d times", calls)
	}
}
