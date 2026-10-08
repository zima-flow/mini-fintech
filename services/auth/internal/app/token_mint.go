package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type tokenMinter struct {
	issuer domain.TokenIssuer
	clock  domain.Clock
	ids    domain.ID
	ttls   TokenTTLs
}

func (m tokenMinter) now() time.Time { return m.clock.Now().UTC() }

func (m tokenMinter) newFamilyID() string { return m.ids.New() }

func (m tokenMinter) mint(ctx context.Context, uow domain.UnitOfWork, user domain.User, familyID string, at time.Time) (domain.TokenPair, string, error) {
	claims := domain.AccessClaims{
		UserID:     user.ID,
		Role:       user.Role,
		CustomerID: user.CustomerID,
		IssuedAt:   at,
		ExpiresAt:  at.Add(m.ttls.Access),
		TokenID:    m.ids.New(),
	}
	accessToken, err := m.issuer.IssueAccess(ctx, claims)
	if err != nil {
		return domain.TokenPair{}, "", fmt.Errorf("issue access token: %w", err)
	}

	secret, err := newRefreshSecret()
	if err != nil {
		return domain.TokenPair{}, "", fmt.Errorf("generate refresh token: %w", err)
	}
	tokenID := m.ids.New()
	if err := uow.RefreshTokens().Create(ctx, domain.RefreshToken{
		ID:        tokenID,
		UserID:    user.ID,
		TokenHash: domain.HashRefreshToken(secret),
		FamilyID:  familyID,
		IssuedAt:  at,
		ExpiresAt: at.Add(m.ttls.Refresh),
	}); err != nil {
		return domain.TokenPair{}, "", fmt.Errorf("store refresh token: %w", err)
	}

	return domain.TokenPair{
		AccessToken:     accessToken,
		RefreshToken:    secret,
		AccessExpiresAt: claims.ExpiresAt,
	}, tokenID, nil
}

func newRefreshSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
