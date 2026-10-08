package token

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type PublishedKey struct {
	Public ed25519.PublicKey
	KeyID  string
}

type Config struct {
	KeyPath       string
	KeyID         string
	Issuer        string
	Audience      string
	PublishedKeys []PublishedKey
}

type Issuer struct {
	key      jwk.Key
	kid      string
	issuer   string
	audience string
	jwks     jwk.Set
}

var _ domain.TokenIssuer = (*Issuer)(nil)

func New(cfg Config) (*Issuer, error) {
	if cfg.KeyPath == "" || cfg.Issuer == "" || cfg.Audience == "" {
		return nil, fmt.Errorf("token: KeyPath, Issuer and Audience are required: %w", errs.ErrFailedPrecondition)
	}

	priv, err := loadPrivateKey(cfg.KeyPath)
	if err != nil {
		return nil, err
	}

	privJWK, err := jwk.Import(priv)
	if err != nil {
		return nil, fmt.Errorf("token: import private key: %w", err)
	}
	pubJWK, err := jwk.Import(priv.Public().(ed25519.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("token: import public key: %w", err)
	}

	kid, err := assignKeyID(pubJWK, cfg.KeyID)
	if err != nil {
		return nil, err
	}
	if err := applyKeyMetadata(privJWK, kid); err != nil {
		return nil, err
	}
	if err := applyKeyMetadata(pubJWK, kid); err != nil {
		return nil, err
	}

	set := jwk.NewSet()
	if err := set.AddKey(pubJWK); err != nil {
		return nil, fmt.Errorf("token: add signing key to jwks: %w", err)
	}
	for _, extra := range cfg.PublishedKeys {
		key, err := publishedJWK(extra)
		if err != nil {
			return nil, err
		}
		if err := set.AddKey(key); err != nil {
			return nil, fmt.Errorf("token: add published key to jwks: %w", err)
		}
	}

	return &Issuer{key: privJWK, kid: kid, issuer: cfg.Issuer, audience: cfg.Audience, jwks: set}, nil
}

func (i *Issuer) IssueAccess(_ context.Context, claims domain.AccessClaims) (string, error) {
	if claims.UserID == "" {
		return "", fmt.Errorf("token: access claims have no subject: %w", errs.ErrInvalidArgument)
	}

	builder := jwt.NewBuilder().
		Issuer(i.issuer).
		Subject(claims.UserID).
		Audience([]string{i.audience}).
		IssuedAt(claims.IssuedAt).
		Expiration(claims.ExpiresAt).
		JwtID(claims.TokenID).
		Claim("role", string(claims.Role))
	if claims.CustomerID != "" {
		builder = builder.Claim("customer_id", claims.CustomerID)
	}

	token, err := builder.Build()
	if err != nil {
		return "", fmt.Errorf("token: build access token: %w", err)
	}
	signed, err := jwt.Sign(token, jwt.WithKey(jwa.EdDSA(), i.key))
	if err != nil {
		return "", fmt.Errorf("token: sign access token: %w", err)
	}
	return string(signed), nil
}

func (i *Issuer) KeyID() string { return i.kid }

func (i *Issuer) PublicJWKS() ([]byte, error) {
	data, err := json.Marshal(i.jwks)
	if err != nil {
		return nil, fmt.Errorf("token: marshal jwks: %w", err)
	}
	return data, nil
}

func assignKeyID(pub jwk.Key, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if err := jwk.AssignKeyID(pub); err != nil {
		return "", fmt.Errorf("token: assign kid: %w", err)
	}
	kid, ok := pub.KeyID()
	if !ok || kid == "" {
		return "", fmt.Errorf("token: kid was not derived: %w", errs.ErrFailedPrecondition)
	}
	return kid, nil
}

func applyKeyMetadata(key jwk.Key, kid string) error {
	if err := key.Set(jwk.KeyIDKey, kid); err != nil {
		return fmt.Errorf("token: set kid: %w", err)
	}
	if err := key.Set(jwk.AlgorithmKey, jwa.EdDSA().String()); err != nil {
		return fmt.Errorf("token: set alg: %w", err)
	}
	return nil
}

func publishedJWK(p PublishedKey) (jwk.Key, error) {
	if len(p.Public) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("token: published key is not Ed25519: %w", errs.ErrFailedPrecondition)
	}
	key, err := jwk.Import(p.Public)
	if err != nil {
		return nil, fmt.Errorf("token: import published key: %w", err)
	}
	kid, err := assignKeyID(key, p.KeyID)
	if err != nil {
		return nil, err
	}
	if err := applyKeyMetadata(key, kid); err != nil {
		return nil, err
	}
	return key, nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("token: read key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("token: decode PEM in %s: %w", path, errs.ErrFailedPrecondition)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("token: parse PKCS#8 key: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("token: key is %T, want ed25519.PrivateKey: %w", parsed, errs.ErrFailedPrecondition)
	}
	return priv, nil
}

func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("token: read public key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("token: decode PEM in %s: %w", path, errs.ErrFailedPrecondition)
	}

	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		pub, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("token: published key is %T, want ed25519.PublicKey: %w", parsed, errs.ErrFailedPrecondition)
		}
		return pub, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("token: parse public key %s: %w", path, errs.ErrFailedPrecondition)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("token: published key is %T, want ed25519.PrivateKey: %w", parsed, errs.ErrFailedPrecondition)
	}
	return priv.Public().(ed25519.PublicKey), nil
}
