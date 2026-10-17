// Package account reads public account privacy settings without caller credentials.
package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MoeclubM/metafusion-community/internal/upstream"
	"github.com/google/uuid"
)

var (
	ErrNotFound    = errors.New("account not found")
	ErrUnavailable = errors.New("account service unavailable")
)

const responseLimit = 64 << 10

type clientIPKey struct{}

// WithClientIP accepts the IP already resolved by the inbound trusted-proxy policy.
// It never accepts a raw forwarding chain or any caller credentials.
func WithClientIP(ctx context.Context, ip string) context.Context {
	if parsed := net.ParseIP(strings.TrimSpace(ip)); parsed != nil {
		return context.WithValue(ctx, clientIPKey{}, parsed.String())
	}
	return ctx
}

type Client struct {
	base string
	up   *upstream.Client
}

func New(base string) *Client {
	p := upstream.DefaultPolicy("account")
	p.Attempts = 2
	p.Budget = 5 * time.Second
	p.DisableRedirects = true
	c := &Client{up: upstream.New(p)}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
		c.base = base
	}
	return c
}

// FavoritesPublic checks the authoritative public profile each time. Missing or
// malformed settings fail closed instead of being treated as a public list.
func (c *Client) FavoritesPublic(ctx context.Context, id string) (bool, error) {
	if c == nil || c.base == "" {
		return false, ErrUnavailable
	}
	wantID, err := uuid.Parse(id)
	if err != nil {
		return false, ErrUnavailable
	}
	headers := http.Header{"Accept": {"application/json"}, "Cache-Control": {"no-store"}}
	if ip, _ := ctx.Value(clientIPKey{}).(string); net.ParseIP(ip) != nil {
		headers.Set("X-Forwarded-For", ip)
	}
	resp, err := c.up.Do(ctx, upstream.Request{
		Method: http.MethodGet,
		URL:    c.base + "/api/users/" + wantID.String(),
		Header: headers,
	})
	if err != nil {
		return false, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return false, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil || len(raw) > responseLimit {
		return false, ErrUnavailable
	}
	var body struct {
		User struct {
			ID              string `json:"id"`
			FavoritesPublic *bool  `json:"favorites_public"`
		} `json:"user"`
	}
	if json.Unmarshal(raw, &body) != nil || body.User.FavoritesPublic == nil {
		return false, ErrUnavailable
	}
	gotID, err := uuid.Parse(body.User.ID)
	if err != nil || gotID != wantID {
		return false, ErrUnavailable
	}
	return *body.User.FavoritesPublic, nil
}
