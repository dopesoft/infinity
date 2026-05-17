// Package auth verifies Supabase-issued JWTs against the project's JWKS
// endpoint and gates HTTP + WebSocket traffic to a single designated owner.
//
// Single-user gate model: the first authenticated user becomes the owner
// (claim recorded in infinity_meta.owner_user_id). Every subsequent request
// must present a JWT whose subject equals that owner UUID. Going multi-tenant
// later is a single boolean flip in Verifier.checkOwner.
//
// Asymmetric verification only - Supabase moved to ES256/RS256 JWKS in 2025.
// We do NOT support legacy HS256 shared-secret verification.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ContextKey is the typed key under which the authenticated user UUID is
// stored in request contexts. Use UserID(ctx) to read it back.
type ContextKey struct{}

// Claims is the subset of Supabase JWT claims we care about.
type Claims struct {
	Email string `json:"email,omitempty"`
	Role  string `json:"role,omitempty"`
	jwt.RegisteredClaims
}

// Verifier is the long-lived auth dependency. Wire one per Server.
type Verifier struct {
	jwks    keyfunc.Keyfunc
	pool    *pgxpool.Pool
	issuer  string // expected `iss` claim, e.g. https://<ref>.supabase.co/auth/v1
	enabled bool

	ownerMu sync.RWMutex
	owner   string // cached owner UUID; "" until first claimant
}

// Config drives the Verifier. JWKSURL and Issuer come from the Supabase
// project URL; the Pool is the same pgxpool the rest of Core uses.
type Config struct {
	JWKSURL string
	Issuer  string
	Pool    *pgxpool.Pool
}

// New builds a Verifier. Returns a disabled verifier (which permits every
// request as a fallback) when JWKSURL is empty so local-dev runs without
// Supabase still work; the doctor command surfaces this state.
func New(ctx context.Context, cfg Config) (*Verifier, error) {
	if cfg.JWKSURL == "" {
		return &Verifier{enabled: false, pool: cfg.Pool}, nil
	}

	k, err := keyfunc.NewDefaultCtx(ctx, []string{cfg.JWKSURL})
	if err != nil {
		return nil, fmt.Errorf("auth: load JWKS from %s: %w", cfg.JWKSURL, err)
	}

	v := &Verifier{
		jwks:    k,
		pool:    cfg.Pool,
		issuer:  cfg.Issuer,
		enabled: true,
	}
	v.refreshOwner(ctx)
	return v, nil
}

// FromEnv constructs a Verifier from SUPABASE_JWKS_URL + SUPABASE_ISSUER.
// SUPABASE_URL alone is enough - issuer + JWKS URL are derivable.
func FromEnv(ctx context.Context, pool *pgxpool.Pool) (*Verifier, error) {
	jwksURL := os.Getenv("SUPABASE_JWKS_URL")
	issuer := os.Getenv("SUPABASE_ISSUER")

	if base := os.Getenv("SUPABASE_URL"); base != "" {
		base = strings.TrimRight(base, "/")
		if jwksURL == "" {
			jwksURL = base + "/auth/v1/.well-known/jwks.json"
		}
		if issuer == "" {
			issuer = base + "/auth/v1"
		}
	}

	return New(ctx, Config{JWKSURL: jwksURL, Issuer: issuer, Pool: pool})
}

// Enabled reports whether JWT verification is active. When false, the gate
// is wide open - useful for local dev only.
func (v *Verifier) Enabled() bool { return v != nil && v.enabled }

// Owner returns the cached owner UUID; empty if no one has claimed yet.
func (v *Verifier) Owner() string {
	v.ownerMu.RLock()
	defer v.ownerMu.RUnlock()
	return v.owner
}

// refreshOwner reads infinity_meta.owner_user_id into the cache. Called at
// startup and after a successful first-claim insert.
func (v *Verifier) refreshOwner(ctx context.Context) {
	if v.pool == nil {
		return
	}
	var owner string
	err := v.pool.QueryRow(ctx,
		`SELECT value FROM infinity_meta WHERE key = 'owner_user_id'`,
	).Scan(&owner)
	if err != nil {
		return // no owner yet - first signup will claim
	}
	v.ownerMu.Lock()
	v.owner = owner
	v.ownerMu.Unlock()
}

// claimOwnerIfUnclaimed atomically inserts the candidate as the owner if no
// owner exists. Returns the canonical owner UUID after the call (either the
// candidate just claimed, or the pre-existing owner). The DB INSERT ON
// CONFLICT DO NOTHING is the actual concurrency primitive - sub-millisecond,
// race-free across processes.
func (v *Verifier) claimOwnerIfUnclaimed(ctx context.Context, candidate string) (string, error) {
	if v.pool == nil {
		return candidate, nil
	}
	_, err := v.pool.Exec(ctx,
		`INSERT INTO infinity_meta (key, value) VALUES ('owner_user_id', $1)
		 ON CONFLICT (key) DO NOTHING`,
		candidate,
	)
	if err != nil {
		return "", err
	}
	var owner string
	err = v.pool.QueryRow(ctx,
		`SELECT value FROM infinity_meta WHERE key = 'owner_user_id'`,
	).Scan(&owner)
	if err != nil {
		return "", err
	}
	v.ownerMu.Lock()
	v.owner = owner
	v.ownerMu.Unlock()
	return owner, nil
}

// VerifyToken parses + validates a Supabase JWT and returns the claims.
// It does NOT enforce the owner check - that's the caller's job (two
// reasons: (a) WS upgrade and HTTP take different code paths into the same
// authz logic, (b) a future debug route may want a verified-but-non-owner
// inspection mode).
func (v *Verifier) VerifyToken(tokenStr string) (*Claims, error) {
	if !v.Enabled() {
		return &Claims{}, nil
	}
	claims := &Claims{}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{"ES256", "RS256"}))
	tok, err := parser.ParseWithClaims(tokenStr, claims, v.jwks.Keyfunc)
	if err != nil {
		return nil, fmt.Errorf("auth: parse token: %w", err)
	}
	if !tok.Valid {
		return nil, errors.New("auth: token invalid")
	}
	if v.issuer != "" && claims.Issuer != v.issuer {
		return nil, fmt.Errorf("auth: bad issuer: %s", claims.Issuer)
	}
	if claims.Subject == "" {
		return nil, errors.New("auth: missing subject")
	}
	if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("auth: token expired")
	}
	return claims, nil
}

// Authorize verifies the token AND enforces the single-user gate. Returns
// the owner UUID on success. First call ever (no owner stored) claims the
// caller as owner - this is the "settings carry-over" point: all existing
// rows with NULL user_id become this user's by convention.
func (v *Verifier) Authorize(ctx context.Context, tokenStr string) (string, error) {
	claims, err := v.VerifyToken(tokenStr)
	if err != nil {
		return "", err
	}
	if !v.Enabled() {
		// Disabled verifier - no gate. Use a static dev UUID so memory writes
		// still get a consistent user_id during local-only runs.
		return "00000000-0000-0000-0000-000000000000", nil
	}

	owner := v.Owner()
	if owner == "" {
		claimed, err := v.claimOwnerIfUnclaimed(ctx, claims.Subject)
		if err != nil {
			return "", fmt.Errorf("auth: claim owner: %w", err)
		}
		owner = claimed
	}

	if claims.Subject != owner {
		return "", fmt.Errorf("auth: user %s is not the owner", claims.Subject)
	}
	return owner, nil
}

// extractToken pulls a JWT from one of three places (in order of preference):
//   1. Authorization: Bearer <jwt>
//   2. Sec-WebSocket-Protocol: bearer.<jwt>
//   3. ?token=<jwt> query param (browsers can't set WS headers)
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if strings.HasPrefix(h, "Bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	if h := r.Header.Get("Sec-WebSocket-Protocol"); h != "" {
		for _, part := range strings.Split(h, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "bearer.") {
				return strings.TrimPrefix(part, "bearer.")
			}
		}
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return q
	}
	return ""
}

// HTTPMiddleware enforces the gate on JSON API routes. Always allows OPTIONS
// and unauthenticated paths in `skipPrefixes` (e.g. /health). 401s on
// missing/invalid tokens, 403s on non-owner.
func (v *Verifier) HTTPMiddleware(skipPrefixes []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			for _, p := range skipPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if !v.Enabled() {
				// Dev mode: still inject a fake user so downstream code that
				// reads UserID(ctx) doesn't panic.
				ctx := context.WithValue(r.Context(), ContextKey{}, "00000000-0000-0000-0000-000000000000")
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			tok := extractToken(r)
			if tok == "" {
				http.Error(w, "unauthorized: no token", http.StatusUnauthorized)
				return
			}
			userID, err := v.Authorize(r.Context(), tok)
			if err != nil {
				if strings.Contains(err.Error(), "not the owner") {
					http.Error(w, err.Error(), http.StatusForbidden)
				} else {
					http.Error(w, err.Error(), http.StatusUnauthorized)
				}
				return
			}
			ctx := context.WithValue(r.Context(), ContextKey{}, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AuthorizeRequest is the WS upgrade entry point. Returns the user UUID on
// success - caller is responsible for closing the connection on error.
func (v *Verifier) AuthorizeRequest(r *http.Request) (string, error) {
	if !v.Enabled() {
		return "00000000-0000-0000-0000-000000000000", nil
	}
	tok := extractToken(r)
	if tok == "" {
		return "", errors.New("no token")
	}
	return v.Authorize(r.Context(), tok)
}

// UserID extracts the authenticated user UUID from a request context.
// Returns "" if the request never went through the auth middleware.
func UserID(ctx context.Context) string {
	v, _ := ctx.Value(ContextKey{}).(string)
	return v
}
