package account

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MoeclubM/metafusion-community/internal/auth"
)

const accountID = "01a10136-c0f1-7eb5-a66c-112270350f28"

func TestFavoritesPublicRequiresExplicitMatchingSetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
		err  error
	}{
		{"public", `{"user":{"id":"` + accountID + `","favorites_public":true}}`, true, nil},
		{"private", `{"user":{"id":"` + accountID + `","favorites_public":false}}`, false, nil},
		{"canonical UUID equality", `{"user":{"id":"` + strings.ToUpper(accountID) + `","favorites_public":true}}`, true, nil},
		{"missing setting", `{"user":{"id":"` + accountID + `"}}`, false, ErrUnavailable},
		{"null setting", `{"user":{"id":"` + accountID + `","favorites_public":null}}`, false, ErrUnavailable},
		{"wrong setting type", `{"user":{"id":"` + accountID + `","favorites_public":"true"}}`, false, ErrUnavailable},
		{"missing user", `{}`, false, ErrUnavailable},
		{"missing ID", `{"user":{"favorites_public":true}}`, false, ErrUnavailable},
		{"wrong ID", `{"user":{"id":"11111111-1111-1111-1111-111111111111","favorites_public":true}}`, false, ErrUnavailable},
		{"malformed", `{"user":`, false, ErrUnavailable},
		{"trailing JSON", `{"user":{"id":"` + accountID + `","favorites_public":true}} {}`, false, ErrUnavailable},
		{"oversized", `{"user":{"id":"` + accountID + `","favorites_public":true},"extra":"` + strings.Repeat("x", responseLimit) + `"}`, false, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/users/"+accountID {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			got, err := New(srv.URL).FavoritesPublic(context.Background(), accountID)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("got %v, %v; want %v, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestFavoritesPublicStatusErrorsAndBoundedRetry(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("sensitive upstream detail"))
			}))
			defer srv.Close()
			c := New(srv.URL)
			c.up.SetSleeper(func(context.Context, time.Duration) error { return nil })
			_, err := c.FavoritesPublic(context.Background(), accountID)
			wantErr, wantCalls := ErrUnavailable, int32(1)
			if status == http.StatusNotFound {
				wantErr = ErrNotFound
			}
			if status >= 500 {
				wantCalls = 2
			}
			if !errors.Is(err, wantErr) || calls.Load() != wantCalls {
				t.Fatalf("error=%v calls=%d; want %v calls=%d", err, calls.Load(), wantErr, wantCalls)
			}
			if strings.Contains(err.Error(), "sensitive") {
				t.Fatal("upstream detail leaked")
			}
		})
	}
}

func TestFavoritesPublicUnavailableConfigurationAndInput(t *testing.T) {
	for _, base := range []string{"", "  ", "://broken", "ftp://example.com", "https://secret:password@example.com", "https://example.com?token=secret", "https://example.com#fragment"} {
		if _, err := New(base).FavoritesPublic(context.Background(), accountID); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("base %q: want unavailable, got %v", base, err)
		}
	}
	var c *Client
	if _, err := c.FavoritesPublic(context.Background(), accountID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client: %v", err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	if _, err := New(srv.URL).FavoritesPublic(context.Background(), "../users"); !errors.Is(err, ErrUnavailable) || calls.Load() != 0 {
		t.Fatalf("invalid ID made request or did not fail: calls=%d err=%v", calls.Load(), err)
	}
}

func TestFavoritesPublicAnonymousAndNeverCaches(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("caller credentials must not reach public account endpoint")
		}
		if r.Header.Get("X-Forwarded-For") != "" {
			t.Error("missing trusted client IP context must not manufacture a forwarded IP")
		}
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("Cache-Control") != "no-store" {
			t.Error("missing public JSON/no-store request headers")
		}
		n := calls.Add(1)
		_, _ = fmt.Fprintf(w, `{"user":{"id":"%s","favorites_public":%t}}`, accountID, n == 1)
	}))
	defer srv.Close()
	c := New(srv.URL + "/")
	ctx := auth.WithCredentials(context.Background(), "private-bearer", "private-cookie")
	for _, want := range []bool{true, false} {
		got, err := c.FavoritesPublic(ctx, accountID)
		if err != nil || got != want {
			t.Fatalf("got %v %v; want %v", got, err, want)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("privacy result cached: %d requests", calls.Load())
	}
}

func TestFavoritesPublicForwardsOnlyValidatedClientIP(t *testing.T) {
	for _, tc := range []struct{ ip, want string }{
		{"198.51.100.12", "198.51.100.12"},
		{"2001:db8::12", "2001:db8::12"},
		{"", ""},
		{"unknown", ""},
		{"198.51.100.12, 127.0.0.1", ""},
		{"198.51.100.12\r\nAuthorization: secret", ""},
	} {
		t.Run(tc.ip, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-Forwarded-For"); got != tc.want {
					t.Errorf("X-Forwarded-For=%q, want %q", got, tc.want)
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("caller credentials forwarded")
				}
				_, _ = fmt.Fprintf(w, `{"user":{"id":"%s","favorites_public":false}}`, accountID)
			}))
			defer srv.Close()
			ctx := WithClientIP(context.Background(), tc.ip)
			if public, err := New(srv.URL).FavoritesPublic(ctx, accountID); err != nil || public {
				t.Fatalf("got public=%v err=%v", public, err)
			}
		})
	}
}

func TestFavoritesPublicRejectsRedirect(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		_, _ = fmt.Fprintf(w, `{"user":{"id":"%s","favorites_public":true}}`, accountID)
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()
	if _, err := New(srv.URL).FavoritesPublic(context.Background(), accountID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirect should be unavailable, got %v", err)
	}
	if reached.Load() != 0 {
		t.Fatal("redirect target was reached")
	}
}
