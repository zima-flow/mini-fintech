package authn

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
)

const (
	DefaultCacheTTL = 5 * time.Minute

	DefaultMinRefreshInterval = time.Minute

	DefaultNegativeCacheTTL = time.Minute

	jwksFetchTimeout = 5 * time.Second
)

type Config struct {
	JWKSURL  string
	CacheTTL time.Duration
	Issuer   string
	Audience string
	Dev      bool
	Clock    clock.Clock
}

func (c Config) Validate() error {
	if c.Dev {
		return nil
	}
	if c.Issuer == "" || c.Audience == "" {
		return fmt.Errorf("authn: Issuer and Audience are required unless Dev is set (rule 07): %w", errs.ErrFailedPrecondition)
	}
	return nil
}

type Claims struct {
	Subject    string
	Roles      []string
	CustomerID string
	ExpiresAt  time.Time
	ID         string
}

type Verifier struct {
	cfg   Config
	clock clock.Clock

	fetchMu sync.Mutex

	mu        sync.Mutex
	keyset    jwk.Set
	fetchedAt time.Time
	gen       uint64

	lastMissAt time.Time
	negatives  map[string]time.Time
}

func NewVerifier(cfg Config) (*Verifier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	return &Verifier{cfg: cfg, clock: clk}, nil
}

func (v *Verifier) now() time.Time { return v.clock.Now().UTC() }

func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
	if raw == "" {
		return Claims{}, fmt.Errorf("authn: empty token: %w", errs.ErrUnauthenticated)
	}

	header, err := tokenHeader(raw)
	if err != nil {
		return Claims{}, err
	}

	key, err := v.keyFor(ctx, header.kid)
	if err != nil {
		return Claims{}, err
	}
	if alg, ok := key.Algorithm(); ok && alg.String() != jwa.EdDSA().String() {
		return Claims{}, fmt.Errorf("authn: key algorithm %q is not EdDSA: %w", alg.String(), errs.ErrUnauthenticated)
	}

	opts := []jwt.ParseOption{jwt.WithKey(jwa.EdDSA(), key)}
	if v.cfg.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.cfg.Issuer))
	}
	if v.cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(v.cfg.Audience))
	}

	token, err := jwt.Parse([]byte(raw), opts...)
	if err != nil {
		return Claims{}, fmt.Errorf("authn: invalid token: %w", errs.ErrUnauthenticated)
	}
	return claimsFromToken(token)
}

type joseHeader struct {
	kid string
}

func tokenHeader(raw string) (joseHeader, error) {
	msg, err := jws.Parse([]byte(raw))
	if err != nil {
		return joseHeader{}, fmt.Errorf("authn: parse jws: %w", errs.ErrUnauthenticated)
	}
	sigs := msg.Signatures()
	if len(sigs) != 1 {
		return joseHeader{}, fmt.Errorf("authn: expected exactly one signature: %w", errs.ErrUnauthenticated)
	}

	hdr := sigs[0].ProtectedHeaders()
	alg, ok := hdr.Algorithm()
	if !ok {
		return joseHeader{}, fmt.Errorf("authn: missing alg: %w", errs.ErrUnauthenticated)
	}
	if alg.String() != jwa.EdDSA().String() {
		return joseHeader{}, fmt.Errorf("authn: algorithm %q is not allowed (EdDSA required): %w", alg.String(), errs.ErrUnauthenticated)
	}

	kid, ok := hdr.KeyID()
	if !ok || kid == "" {
		return joseHeader{}, fmt.Errorf("authn: missing kid: %w", errs.ErrUnauthenticated)
	}
	return joseHeader{kid: kid}, nil
}

func (v *Verifier) keyFor(ctx context.Context, kid string) (jwk.Key, error) {
	now := v.now()

	set, attempted, err := v.keySet(ctx, now, false)
	if err != nil {
		return nil, err
	}
	if key, ok := set.LookupKeyID(kid); ok {
		return key, nil
	}
	if v.negativelyCached(kid, now) {
		return nil, unknownKid(kid)
	}
	if attempted || !v.refreshAllowed(now) {
		v.recordMiss(kid, now)
		return nil, unknownKid(kid)
	}

	refreshed, _, err := v.keySet(ctx, now, true)
	if err != nil {
		return nil, err
	}
	if key, ok := refreshed.LookupKeyID(kid); ok {
		v.clearNegative(kid)
		return key, nil
	}
	v.recordMiss(kid, now)
	return nil, unknownKid(kid)
}

func (v *Verifier) keySet(ctx context.Context, now time.Time, force bool) (set jwk.Set, attempted bool, err error) {
	v.mu.Lock()
	if !force && v.keyset != nil && now.Sub(v.fetchedAt) < v.ttl() {
		cached := v.keyset
		v.mu.Unlock()
		return cached, false, nil
	}
	gen := v.gen
	v.mu.Unlock()

	v.fetchMu.Lock()
	defer v.fetchMu.Unlock()

	v.mu.Lock()
	if v.gen != gen && v.keyset != nil {
		cached := v.keyset
		v.mu.Unlock()
		return cached, true, nil
	}
	stale := v.keyset
	v.mu.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, jwksFetchTimeout)
	defer cancel()

	fetched, fetchErr := jwk.Fetch(fetchCtx, v.cfg.JWKSURL)
	if fetchErr != nil {
		if stale != nil {
			return stale, true, nil
		}
		return nil, true, fmt.Errorf("authn: fetch jwks: %w", errors.Join(fetchErr, errs.ErrUnavailable))
	}

	v.mu.Lock()
	v.keyset = fetched
	v.fetchedAt = now
	v.gen++
	v.mu.Unlock()
	return fetched, true, nil
}

func (v *Verifier) refreshAllowed(now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return now.Sub(v.lastMissAt) >= DefaultMinRefreshInterval
}

func (v *Verifier) negativelyCached(kid string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	expiry, ok := v.negatives[kid]
	return ok && now.Before(expiry)
}

func (v *Verifier) recordMiss(kid string, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lastMissAt = now
	if v.negatives == nil {
		v.negatives = make(map[string]time.Time)
	}
	v.negatives[kid] = now.Add(DefaultNegativeCacheTTL)
}

func (v *Verifier) clearNegative(kid string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.negatives, kid)
}

func unknownKid(kid string) error {
	return fmt.Errorf("authn: unknown kid %q: %w", kid, errs.ErrUnauthenticated)
}

func (v *Verifier) ttl() time.Duration {
	if v.cfg.CacheTTL > 0 {
		return v.cfg.CacheTTL
	}
	return DefaultCacheTTL
}

func claimsFromToken(token jwt.Token) (Claims, error) {
	subject, ok := token.Subject()
	if !ok || subject == "" {
		return Claims{}, fmt.Errorf("authn: token has no subject: %w", errs.ErrUnauthenticated)
	}
	expiresAt, ok := token.Expiration()
	if !ok {
		return Claims{}, fmt.Errorf("authn: token has no expiry: %w", errs.ErrUnauthenticated)
	}

	claims := Claims{
		Subject:   subject,
		Roles:     rolesFromToken(token),
		ExpiresAt: expiresAt,
	}
	if id, ok := token.JwtID(); ok {
		claims.ID = id
	}
	var customerID string
	if err := token.Get("customer_id", &customerID); err == nil {
		claims.CustomerID = customerID
	}
	return claims, nil
}

func rolesFromToken(token jwt.Token) []string {
	var role string
	if err := token.Get("role", &role); err != nil || role == "" {
		return nil
	}
	return []string{role}
}
