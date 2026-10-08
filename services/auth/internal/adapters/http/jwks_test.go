package authhttp_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"

	authhttp "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/http"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/token"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

func TestHandler_ServesJWKSMatchingSignedTokens(t *testing.T) {
	t.Parallel()

	iss := newIssuer(t)
	srv := httptest.NewServer(authhttp.Handler(iss))
	defer srv.Close()

	resp, err := http.Get(srv.URL + authhttp.JWKSPath)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	set, err := jwk.Parse(data)
	require.NoError(t, err)

	key, ok := set.LookupKeyID(iss.KeyID())
	require.True(t, ok, "the JWKS must publish the signing kid")

	now := time.Now().UTC().Truncate(time.Second)
	raw, err := iss.IssueAccess(context.Background(), domain.AccessClaims{
		UserID:    "user-1",
		Role:      domain.RoleClient,
		IssuedAt:  now,
		ExpiresAt: now.Add(15 * time.Minute),
		TokenID:   "jti-1",
	})
	require.NoError(t, err)
	_, err = jwt.Parse([]byte(raw), jwt.WithKey(jwa.EdDSA(), key))
	require.NoError(t, err, "a token signed by the issuer must verify against the advertised JWKS")
}

func TestHandler_UnknownPath_NotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(authhttp.Handler(newIssuer(t)))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nope")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandler_NonGET_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(authhttp.Handler(newIssuer(t)))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+authhttp.JWKSPath, nil)
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestHandler_SourceError_InternalServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(authhttp.Handler(errSource{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + authhttp.JWKSPath)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

type errSource struct{}

func (errSource) PublicJWKS() ([]byte, error) { return nil, errors.New("boom") }

func newIssuer(t *testing.T) *token.Issuer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "auth_ed25519_private.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))

	iss, err := token.New(token.Config{KeyPath: path, Issuer: "mini-fintech-auth", Audience: "mini-fintech"})
	require.NoError(t, err)
	return iss
}
