package token_test

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/token"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

const (
	testIssuer   = "mini-fintech-auth"
	testAudience = "mini-fintech"
)

func testInstant() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

func writeEd25519Key(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	path := filepath.Join(t.TempDir(), "auth_ed25519_private.pem")
	require.NoError(t, os.WriteFile(path, block, 0o600))
	return path, priv
}

func mustIssuer(t *testing.T, path string) *token.Issuer {
	t.Helper()
	iss, err := token.New(token.Config{KeyPath: path, Issuer: testIssuer, Audience: testAudience})
	require.NoError(t, err)
	return iss
}

func TestIssuer_SignVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	path, priv := writeEd25519Key(t)
	iss := mustIssuer(t, path)
	require.Len(t, iss.KeyID(), 43, "an Ed25519 SHA-256 thumbprint is 32 bytes of base64url")
	require.NotContains(t, iss.KeyID(), "=")

	now := testInstant()
	raw, err := iss.IssueAccess(context.Background(), domain.AccessClaims{
		UserID:     "user-1",
		Role:       domain.RoleClient,
		CustomerID: "cust-1",
		IssuedAt:   now,
		ExpiresAt:  now.Add(15 * time.Minute),
		TokenID:    "jti-1",
	})
	require.NoError(t, err)

	msg, err := jws.Parse([]byte(raw))
	require.NoError(t, err)
	require.Len(t, msg.Signatures(), 1)
	hdr := msg.Signatures()[0].ProtectedHeaders()
	alg, ok := hdr.Algorithm()
	require.True(t, ok)
	require.Equal(t, jwa.EdDSA().String(), alg.String())
	kid, ok := hdr.KeyID()
	require.True(t, ok)
	require.Equal(t, iss.KeyID(), kid)

	// The token verifies against the derived public key and carries the claims.
	pubJWK, err := jwk.Import(priv.Public().(ed25519.PublicKey))
	require.NoError(t, err)
	tok, err := jwt.Parse([]byte(raw), jwt.WithKey(jwa.EdDSA(), pubJWK))
	require.NoError(t, err)

	require.Equal(t, "user-1", claimString(t, tok, "sub"))
	require.Equal(t, testIssuer, claimString(t, tok, "iss"))
	require.Equal(t, "jti-1", claimString(t, tok, "jti"))
	require.Equal(t, "CLIENT", claimString(t, tok, "role"))
	require.Equal(t, "cust-1", claimString(t, tok, "customer_id"))
	iat, ok := tok.IssuedAt()
	require.True(t, ok)
	require.Equal(t, now, iat)
	exp, ok := tok.Expiration()
	require.True(t, ok)
	require.Equal(t, now.Add(15*time.Minute), exp)

	thumbprint, err := pubJWK.Thumbprint(crypto.SHA256)
	require.NoError(t, err)
	require.Equal(t, base64.RawURLEncoding.EncodeToString(thumbprint), iss.KeyID())
}

func TestIssuer_CustomerIDOmittedUntilLinked(t *testing.T) {
	t.Parallel()

	path, priv := writeEd25519Key(t)
	iss := mustIssuer(t, path)

	now := testInstant()
	raw, err := iss.IssueAccess(context.Background(), domain.AccessClaims{
		UserID:    "user-1",
		Role:      domain.RoleClient,
		IssuedAt:  now,
		ExpiresAt: now.Add(15 * time.Minute),
		TokenID:   "jti-1",
	})
	require.NoError(t, err)

	pubJWK, err := jwk.Import(priv.Public().(ed25519.PublicKey))
	require.NoError(t, err)
	tok, err := jwt.Parse([]byte(raw), jwt.WithKey(jwa.EdDSA(), pubJWK))
	require.NoError(t, err)

	require.False(t, tok.Has("customer_id"), "customer_id must be absent until linked")
}

func TestIssuer_ExpiredToken_Rejected(t *testing.T) {
	t.Parallel()

	path, priv := writeEd25519Key(t)
	iss := mustIssuer(t, path)

	past := testInstant().Add(-time.Hour)
	raw, err := iss.IssueAccess(context.Background(), domain.AccessClaims{
		UserID:    "user-1",
		Role:      domain.RoleClient,
		IssuedAt:  past,
		ExpiresAt: past.Add(time.Minute),
		TokenID:   "jti-1",
	})
	require.NoError(t, err)

	pubJWK, err := jwk.Import(priv.Public().(ed25519.PublicKey))
	require.NoError(t, err)
	_, err = jwt.Parse([]byte(raw), jwt.WithKey(jwa.EdDSA(), pubJWK))
	require.Error(t, err, "an expired token must not validate")
}

func TestIssuer_MissingSubject_InvalidArgument(t *testing.T) {
	t.Parallel()

	path, _ := writeEd25519Key(t)
	iss := mustIssuer(t, path)

	_, err := iss.IssueAccess(context.Background(), domain.AccessClaims{
		Role:      domain.RoleClient,
		IssuedAt:  testInstant(),
		ExpiresAt: testInstant().Add(15 * time.Minute),
		TokenID:   "jti-1",
	})
	require.Error(t, err)
}

func TestIssuer_PublicJWKS_ValidForVerification(t *testing.T) {
	t.Parallel()

	path, _ := writeEd25519Key(t)
	iss := mustIssuer(t, path)

	data, err := iss.PublicJWKS()
	require.NoError(t, err)

	set, err := jwk.Parse(data)
	require.NoError(t, err)
	require.Equal(t, 1, set.Len())

	key, ok := set.LookupKeyID(iss.KeyID())
	require.True(t, ok, "the JWKS must publish the signing kid")
	require.Equal(t, jwa.EdDSA().String(), keyAlgorithm(t, key))
	require.False(t, key.Has("d"), "only public material is published")

	raw, err := iss.IssueAccess(context.Background(), domain.AccessClaims{
		UserID:    "user-1",
		Role:      domain.RoleAdmin,
		IssuedAt:  testInstant(),
		ExpiresAt: testInstant().Add(15 * time.Minute),
		TokenID:   "jti-1",
	})
	require.NoError(t, err)
	_, err = jwt.Parse([]byte(raw), jwt.WithKey(jwa.EdDSA(), key))
	require.NoError(t, err)
}

func TestIssuer_ExtraPublishedKeys(t *testing.T) {
	t.Parallel()

	path, priv := writeEd25519Key(t)
	extraPub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	extraJWK, err := jwk.Import(extraPub)
	require.NoError(t, err)
	require.NoError(t, jwk.AssignKeyID(extraJWK))
	extraKID, _ := extraJWK.KeyID()

	iss, err := token.New(token.Config{
		KeyPath:       path,
		Issuer:        testIssuer,
		Audience:      testAudience,
		PublishedKeys: []token.PublishedKey{{Public: extraPub}},
	})
	require.NoError(t, err)

	data, err := iss.PublicJWKS()
	require.NoError(t, err)
	set, err := jwk.Parse(data)
	require.NoError(t, err)
	require.Equal(t, 2, set.Len())
	_, ok := set.LookupKeyID(iss.KeyID())
	require.True(t, ok)
	_, ok = set.LookupKeyID(extraKID)
	require.True(t, ok, "the extra published key is advertised")

	raw, err := iss.IssueAccess(context.Background(), domain.AccessClaims{
		UserID:    "user-1",
		Role:      domain.RoleClient,
		IssuedAt:  testInstant(),
		ExpiresAt: testInstant().Add(15 * time.Minute),
		TokenID:   "jti-1",
	})
	require.NoError(t, err)
	pubJWK, err := jwk.Import(priv.Public().(ed25519.PublicKey))
	require.NoError(t, err)
	_, err = jwt.Parse([]byte(raw), jwt.WithKey(jwa.EdDSA(), pubJWK))
	require.NoError(t, err)
}

func TestNew_ExplicitKeyID(t *testing.T) {
	t.Parallel()

	path, _ := writeEd25519Key(t)
	iss, err := token.New(token.Config{
		KeyPath:  path,
		KeyID:    "rotation-2026-10",
		Issuer:   testIssuer,
		Audience: testAudience,
	})
	require.NoError(t, err)
	require.Equal(t, "rotation-2026-10", iss.KeyID())

	data, err := iss.PublicJWKS()
	require.NoError(t, err)
	set, err := jwk.Parse(data)
	require.NoError(t, err)
	_, ok := set.LookupKeyID("rotation-2026-10")
	require.True(t, ok)
}

func TestNew_InvalidInput(t *testing.T) {
	t.Parallel()

	goodPath, _ := writeEd25519Key(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	require.NoError(t, err)
	rsaPath := filepath.Join(t.TempDir(), "rsa.pem")
	require.NoError(t, os.WriteFile(rsaPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER}), 0o600))

	junkPath := filepath.Join(t.TempDir(), "junk.pem")
	require.NoError(t, os.WriteFile(junkPath, []byte("not a pem"), 0o600))

	tests := []struct {
		name string
		cfg  token.Config
	}{
		{name: "missing key path", cfg: token.Config{Issuer: testIssuer, Audience: testAudience}},
		{name: "missing issuer", cfg: token.Config{KeyPath: goodPath, Audience: testAudience}},
		{name: "missing audience", cfg: token.Config{KeyPath: goodPath, Issuer: testIssuer}},
		{name: "nonexistent file", cfg: token.Config{KeyPath: filepath.Join(t.TempDir(), "nope.pem"), Issuer: testIssuer, Audience: testAudience}},
		{name: "not pem", cfg: token.Config{KeyPath: junkPath, Issuer: testIssuer, Audience: testAudience}},
		{name: "not ed25519", cfg: token.Config{KeyPath: rsaPath, Issuer: testIssuer, Audience: testAudience}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := token.New(tc.cfg)
			require.Error(t, err)
		})
	}
}

func TestLoadPublicKey_Ed25519PublicPEM(t *testing.T) {
	t.Parallel()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "old_ed25519_public.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))

	got, err := token.LoadPublicKey(path)
	require.NoError(t, err)
	require.Equal(t, pub, got)
}

func TestLoadPublicKey_RejectsNonEd25519(t *testing.T) {
	t.Parallel()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "rsa_public.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))

	_, err = token.LoadPublicKey(path)
	require.Error(t, err)
}

func TestLoadPublicKey_InvalidInput(t *testing.T) {
	t.Parallel()

	junk := filepath.Join(t.TempDir(), "junk.pem")
	require.NoError(t, os.WriteFile(junk, []byte("not a pem"), 0o600))

	tests := []struct {
		name string
		path string
	}{
		{name: "nonexistent", path: filepath.Join(t.TempDir(), "nope.pem")},
		{name: "not pem", path: junk},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := token.LoadPublicKey(tc.path)
			require.Error(t, err)
		})
	}
}

// --- helpers ---

func claimString(t *testing.T, tok jwt.Token, name string) string {
	t.Helper()
	var value string
	require.NoError(t, tok.Get(name, &value))
	return value
}

func keyAlgorithm(t *testing.T, key jwk.Key) string {
	t.Helper()
	alg, ok := key.Algorithm()
	require.True(t, ok)
	return alg.String()
}
