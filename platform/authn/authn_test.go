package authn_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

const (
	testKID      = "test-kid"
	testIssuer   = "mini-fintech-auth"
	testAudience = "mini-fintech"
)

func TestVerifier_ValidToken_ReturnsClaims(t *testing.T) {
	t.Parallel()

	priv := mustEd25519(t)
	srv := newJWKSServer(t)
	srv.setKey(t, priv.Public().(ed25519.PublicKey), testKID)
	v := mustVerifier(t, srv.url)

	expiry := time.Now().Add(15 * time.Minute)
	token := signEdDSAClaims(t, priv, testKID, expiry, testIssuer, testAudience, "CLIENT", "customer-7")

	claims, err := v.Verify(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.Subject)
	require.Equal(t, []string{"CLIENT"}, claims.Roles)
	require.Equal(t, "customer-7", claims.CustomerID)
	require.Equal(t, "jti-1", claims.ID)
	require.WithinDuration(t, expiry, claims.ExpiresAt, time.Minute)
}

func TestVerifier_RejectsBadTokens(t *testing.T) {
	t.Parallel()

	priv := mustEd25519(t)
	other := mustEd25519(t)
	srv := newJWKSServer(t)
	srv.setKey(t, priv.Public().(ed25519.PublicKey), testKID)
	v := mustVerifier(t, srv.url)

	expiry := time.Now().Add(15 * time.Minute)
	valid := signEdDSAClaims(t, priv, testKID, expiry, testIssuer, testAudience, "CLIENT", "")

	cases := map[string]string{
		"expired":        signEdDSAClaims(t, priv, testKID, time.Now().Add(-time.Minute), testIssuer, testAudience, "CLIENT", ""),
		"wrong_issuer":   signEdDSAClaims(t, priv, testKID, expiry, "someone-else", testAudience, "CLIENT", ""),
		"wrong_audience": signEdDSAClaims(t, priv, testKID, expiry, testIssuer, "someone-else", "CLIENT", ""),
		"alg_none":       noneToken(t, testKID),
		"alg_rs256":      rs256Token(t, testKID, expiry),
		"missing_kid":    signEdDSAClaims(t, priv, "", expiry, testIssuer, testAudience, "CLIENT", ""),
		"unknown_kid":    signEdDSAClaims(t, other, "unknown-kid", expiry, testIssuer, testAudience, "CLIENT", ""),
		"garbage":        "not.a.jwt",
		"empty":          "",
		"valid":          valid,
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := v.Verify(context.Background(), token)
			if name == "valid" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, errs.ErrUnauthenticated, "err = %v", err)
		})
	}
}

func TestVerifier_JWKSFailure_IsUnavailableAndNotCached(t *testing.T) {
	t.Parallel()

	priv := mustEd25519(t)
	srv := newJWKSServer(t)
	srv.setKey(t, priv.Public().(ed25519.PublicKey), testKID)
	srv.fail(http.StatusInternalServerError)
	v := mustVerifier(t, srv.url)

	token := signEdDSAClaims(t, priv, testKID, time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")

	_, err := v.Verify(context.Background(), token)
	require.ErrorIs(t, err, errs.ErrUnavailable, "err = %v", err)

	srv.recover()
	claims, err := v.Verify(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.Subject)
}

func TestVerifier_UnknownKid_RefreshesJWKS(t *testing.T) {
	t.Parallel()

	keyA := mustEd25519(t)
	keyB := mustEd25519(t)
	srv := newJWKSServer(t)
	srv.setKey(t, keyA.Public().(ed25519.PublicKey), "kid-a")
	v := mustVerifier(t, srv.url)

	tokenA := signEdDSAClaims(t, keyA, "kid-a", time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")
	_, err := v.Verify(context.Background(), tokenA)
	require.NoError(t, err, "initial key must verify and cache")

	srv.setKey(t, keyB.Public().(ed25519.PublicKey), "kid-b")
	tokenB := signEdDSAClaims(t, keyB, "kid-b", time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")

	claims, err := v.Verify(context.Background(), tokenB)
	require.NoError(t, err, "an unknown kid must trigger a JWKS refresh")
	require.Equal(t, "user-1", claims.Subject)
}

func TestVerifier_UnknownKidRefreshesAreThrottled(t *testing.T) {
	t.Parallel()

	priv := mustEd25519(t)
	other := mustEd25519(t)
	srv := newJWKSServer(t)
	srv.setKey(t, priv.Public().(ed25519.PublicKey), "kid-a")
	clk := newFakeClock(time.Now())
	v := mustVerifierWithClock(t, srv.url, clk)

	known := signEdDSAClaims(t, priv, "kid-a", time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")
	_, err := v.Verify(context.Background(), known)
	require.NoError(t, err)
	require.Equal(t, 1, srv.requestCount(), "initial fetch caches kid-a")

	for i := 0; i < 10; i++ {
		token := signEdDSAClaims(t, other, fmt.Sprintf("kid-x-%d", i), time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")
		_, err := v.Verify(context.Background(), token)
		require.ErrorIs(t, err, errs.ErrUnauthenticated)
	}
	require.Equal(t, 2, srv.requestCount(), "a burst of unknown kids must cause at most one refresh")

	clk.Advance(2 * time.Minute)
	token := signEdDSAClaims(t, other, "kid-later", time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")
	_, err = v.Verify(context.Background(), token)
	require.ErrorIs(t, err, errs.ErrUnauthenticated)
	require.Equal(t, 3, srv.requestCount(), "the throttle window is time-bounded")
}

func TestVerifier_UnknownKidNegativeCache(t *testing.T) {
	t.Parallel()

	priv := mustEd25519(t)
	other := mustEd25519(t)
	srv := newJWKSServer(t)
	srv.setKey(t, priv.Public().(ed25519.PublicKey), "kid-a")
	clk := newFakeClock(time.Now())
	v := mustVerifierWithClock(t, srv.url, clk)

	known := signEdDSAClaims(t, priv, "kid-a", time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")
	_, err := v.Verify(context.Background(), known)
	require.NoError(t, err)

	unknown := signEdDSAClaims(t, other, "kid-ghost", time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")
	_, err = v.Verify(context.Background(), unknown)
	require.ErrorIs(t, err, errs.ErrUnauthenticated)
	require.Equal(t, 2, srv.requestCount(), "the first miss refreshes once")

	for i := 0; i < 10; i++ {
		_, err := v.Verify(context.Background(), unknown)
		require.ErrorIs(t, err, errs.ErrUnauthenticated)
	}
	require.Equal(t, 2, srv.requestCount(), "a repeated unknown kid must be served from the negative cache")
}

func TestVerifier_ConcurrentMisses_FetchOnce(t *testing.T) {
	t.Parallel()

	priv := mustEd25519(t)
	srv := newJWKSServer(t)
	srv.setKey(t, priv.Public().(ed25519.PublicKey), testKID)
	v := mustVerifier(t, srv.url)
	token := signEdDSAClaims(t, priv, testKID, time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")

	release := srv.hold()
	defer release()

	const n = 25
	start := make(chan struct{})
	results := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := v.Verify(context.Background(), token)
			results <- err
		}()
	}
	close(start)

	assert.Never(t, func() bool { return srv.requestCount() > 1 }, 200*time.Millisecond, 10*time.Millisecond,
		"concurrent misses must share a single in-flight fetch")

	release()
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.Equal(t, 1, srv.requestCount())
}

func TestVerifier_ServesStaleKeysetWhenRefreshFails(t *testing.T) {
	t.Parallel()

	priv := mustEd25519(t)
	srv := newJWKSServer(t)
	srv.setKey(t, priv.Public().(ed25519.PublicKey), testKID)
	clk := newFakeClock(time.Now())
	v := mustVerifierWithClock(t, srv.url, clk)

	token := signEdDSAClaims(t, priv, testKID, time.Now().Add(15*time.Minute), testIssuer, testAudience, "CLIENT", "")
	_, err := v.Verify(context.Background(), token)
	require.NoError(t, err)

	clk.Advance(11 * time.Minute)
	srv.fail(http.StatusInternalServerError)

	claims, err := v.Verify(context.Background(), token)
	require.NoError(t, err, "a transient JWKS failure must serve the stale keyset")
	require.Equal(t, "user-1", claims.Subject)
}

func TestNewVerifier_MissingIssuerAudience_FailsClosed(t *testing.T) {
	t.Parallel()

	_, err := authn.NewVerifier(authn.Config{JWKSURL: "http://example.invalid"})
	require.ErrorIs(t, err, errs.ErrFailedPrecondition, "err = %v", err)

	_, err = authn.NewVerifier(authn.Config{Issuer: testIssuer, JWKSURL: "http://example.invalid"})
	require.Error(t, err, "missing audience must be invalid")

	_, err = authn.NewVerifier(authn.Config{Audience: testAudience, JWKSURL: "http://example.invalid"})
	require.Error(t, err, "missing issuer must be invalid")

	_, err = authn.NewVerifier(authn.Config{JWKSURL: "http://example.invalid", Dev: true})
	require.NoError(t, err, "dev relaxes the requirement")
}

// --- helpers ---

func mustVerifier(t *testing.T, jwksURL string) *authn.Verifier {
	t.Helper()
	v, err := authn.NewVerifier(authn.Config{
		JWKSURL:  jwksURL,
		CacheTTL: 10 * time.Minute,
		Issuer:   testIssuer,
		Audience: testAudience,
	})
	require.NoError(t, err)
	return v
}

func mustVerifierWithClock(t *testing.T, jwksURL string, clk clock.Clock) *authn.Verifier {
	t.Helper()
	v, err := authn.NewVerifier(authn.Config{
		JWKSURL:  jwksURL,
		CacheTTL: 10 * time.Minute,
		Issuer:   testIssuer,
		Audience: testAudience,
		Clock:    clk,
	})
	require.NoError(t, err)
	return v
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock { return &fakeClock{now: now} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.UTC()
}

func (c *fakeClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func mustEd25519(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return priv
}

func signEdDSAClaims(t *testing.T, priv ed25519.PrivateKey, kid string, expiry time.Time, issuer, audience, role, customerID string) string {
	t.Helper()

	privJWK, err := jwk.Import(priv)
	require.NoError(t, err)
	require.NoError(t, privJWK.Set(jwk.AlgorithmKey, jwa.EdDSA().String()))
	if kid != "" {
		require.NoError(t, privJWK.Set(jwk.KeyIDKey, kid))
	}

	builder := jwt.NewBuilder().
		Issuer(issuer).
		Subject("user-1").
		Audience([]string{audience}).
		IssuedAt(time.Now()).
		JwtID("jti-1").
		Expiration(expiry).
		Claim("role", role)
	if customerID != "" {
		builder = builder.Claim("customer_id", customerID)
	}
	token, err := builder.Build()
	require.NoError(t, err)

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.EdDSA(), privJWK))
	require.NoError(t, err)
	return string(signed)
}

func noneToken(t *testing.T, kid string) string {
	t.Helper()
	header := map[string]any{"alg": "none", "typ": "JWT", "kid": kid}
	payload := map[string]any{
		"iss": testIssuer,
		"aud": []string{testAudience},
		"sub": "user-1",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}
	return encodeSegment(t, header) + "." + encodeSegment(t, payload) + "."
}

func rs256Token(t *testing.T, kid string, expiry time.Time) string {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privJWK, err := jwk.Import(priv)
	require.NoError(t, err)
	require.NoError(t, privJWK.Set(jwk.KeyIDKey, kid))

	token, err := jwt.NewBuilder().
		Issuer(testIssuer).
		Subject("user-1").
		Audience([]string{testAudience}).
		Expiration(expiry).
		Build()
	require.NoError(t, err)

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.RS256(), privJWK))
	require.NoError(t, err)
	return string(signed)
}

func encodeSegment(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(raw)
}

type jwksServer struct {
	url string

	mu       sync.Mutex
	body     []byte
	status   int
	requests int
	gate     chan struct{}
}

func newJWKSServer(t *testing.T) *jwksServer {
	t.Helper()
	s := &jwksServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.requests++
		status := s.status
		body := s.body
		gate := s.gate
		s.mu.Unlock()

		if gate != nil {
			<-gate
		}
		if status != 0 {
			http.Error(w, "jwks error", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *jwksServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *jwksServer) hold() (release func()) {
	gate := make(chan struct{})
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.gate = nil
			s.mu.Unlock()
			close(gate)
		})
	}
}

func (s *jwksServer) setKey(t *testing.T, pub ed25519.PublicKey, kid string) {
	t.Helper()

	key, err := jwk.Import(pub)
	require.NoError(t, err)
	require.NoError(t, key.Set(jwk.KeyIDKey, kid))
	require.NoError(t, key.Set(jwk.AlgorithmKey, jwa.EdDSA().String()))

	set := jwk.NewSet()
	require.NoError(t, set.AddKey(key))
	body, err := json.Marshal(set)
	require.NoError(t, err)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.body = body
	s.status = 0
}

func (s *jwksServer) fail(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *jwksServer) recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = 0
}
