package llm

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OAuthToken is the persisted credential row used by OAuth-backed providers.
// AccessToken is the bearer used on inference calls; RefreshToken rotates on
// every refresh and replaces this row in place. ExpiresAt is the access_token
// expiry - refresh ~2 minutes before to absorb clock skew.
type OAuthToken struct {
	ID            string
	Provider      string
	AccountID     string
	AccountEmail  string
	AccessToken   string
	RefreshToken  string
	IDToken       string
	TokenType     string
	Scope         string
	ExpiresAt     *time.Time
	LastRefreshed time.Time
	UpdatedAt     time.Time
}

// OAuthSession is the short-lived PKCE state row created when the user kicks
// off the connect flow. Looked up by State when the user pastes back the code.
type OAuthSession struct {
	State        string
	Provider     string
	CodeVerifier string
	RedirectURI  string
	ExpiresAt    time.Time
}

// OAuthStore is the Postgres-backed repo for OAuth sessions + tokens. Used by
// both the provider (read access tokens on every call, write on refresh) and
// the HTTP layer (create session, exchange code → store token, status, delete).
type OAuthStore struct {
	pool *pgxpool.Pool
}

func NewOAuthStore(pool *pgxpool.Pool) *OAuthStore {
	return &OAuthStore{pool: pool}
}

var ErrNoToken = errors.New("no oauth token stored for provider")

// --- Sessions ---------------------------------------------------------------

func (s *OAuthStore) CreateSession(ctx context.Context, sess OAuthSession) error {
	if s == nil || s.pool == nil {
		return errors.New("oauth store not configured")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_oauth_sessions (state, provider, code_verifier, redirect_uri, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (state) DO UPDATE SET
			code_verifier = EXCLUDED.code_verifier,
			redirect_uri  = EXCLUDED.redirect_uri,
			expires_at    = EXCLUDED.expires_at
	`, sess.State, sess.Provider, sess.CodeVerifier, sess.RedirectURI, sess.ExpiresAt)
	return err
}

func (s *OAuthStore) ConsumeSession(ctx context.Context, state string) (OAuthSession, error) {
	if s == nil || s.pool == nil {
		return OAuthSession{}, errors.New("oauth store not configured")
	}
	var sess OAuthSession
	err := s.pool.QueryRow(ctx, `
		DELETE FROM mem_oauth_sessions
		WHERE state = $1 AND expires_at > now()
		RETURNING state, provider, code_verifier, redirect_uri, expires_at
	`, state).Scan(&sess.State, &sess.Provider, &sess.CodeVerifier, &sess.RedirectURI, &sess.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthSession{}, errors.New("oauth session expired or unknown")
	}
	return sess, err
}

func (s *OAuthStore) PruneSessions(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM mem_oauth_sessions WHERE expires_at <= now()`)
	return err
}

// --- Tokens -----------------------------------------------------------------

func (s *OAuthStore) UpsertToken(ctx context.Context, t OAuthToken) error {
	if s == nil || s.pool == nil {
		return errors.New("oauth store not configured")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_provider_tokens
			(provider, account_id, account_email, access_token, refresh_token,
			 id_token, token_type, scope, expires_at, last_refreshed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (provider, account_id) DO UPDATE SET
			account_email  = EXCLUDED.account_email,
			access_token   = EXCLUDED.access_token,
			refresh_token  = CASE WHEN EXCLUDED.refresh_token <> ''
			                      THEN EXCLUDED.refresh_token
			                      ELSE mem_provider_tokens.refresh_token END,
			id_token       = CASE WHEN EXCLUDED.id_token <> ''
			                      THEN EXCLUDED.id_token
			                      ELSE mem_provider_tokens.id_token END,
			token_type     = EXCLUDED.token_type,
			scope          = CASE WHEN EXCLUDED.scope <> ''
			                      THEN EXCLUDED.scope
			                      ELSE mem_provider_tokens.scope END,
			expires_at     = EXCLUDED.expires_at,
			last_refreshed = now()
	`, t.Provider, t.AccountID, t.AccountEmail, t.AccessToken, t.RefreshToken,
		t.IDToken, t.TokenType, t.Scope, t.ExpiresAt)
	return err
}

// GetToken returns the most-recently-updated token row for the provider. We
// scope to one account per provider for the single-user model; if that ever
// changes, switch to a (provider, account_id) lookup.
func (s *OAuthStore) GetToken(ctx context.Context, provider string) (OAuthToken, error) {
	if s == nil || s.pool == nil {
		return OAuthToken{}, errors.New("oauth store not configured")
	}
	var t OAuthToken
	err := s.pool.QueryRow(ctx, `
		SELECT id, provider, account_id, account_email, access_token, refresh_token,
		       id_token, token_type, scope, expires_at, last_refreshed, updated_at
		  FROM mem_provider_tokens
		 WHERE provider = $1
		 ORDER BY updated_at DESC
		 LIMIT 1
	`, provider).Scan(&t.ID, &t.Provider, &t.AccountID, &t.AccountEmail,
		&t.AccessToken, &t.RefreshToken, &t.IDToken, &t.TokenType, &t.Scope,
		&t.ExpiresAt, &t.LastRefreshed, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthToken{}, ErrNoToken
	}
	return t, err
}

func (s *OAuthStore) DeleteToken(ctx context.Context, provider string) error {
	if s == nil || s.pool == nil {
		return errors.New("oauth store not configured")
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM mem_provider_tokens WHERE provider = $1`, provider)
	return err
}
