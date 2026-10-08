package app

import (
	"context"
	"fmt"
	"time"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/authn"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (authn.Claims, error)
}

var _ TokenVerifier = (*authn.Verifier)(nil)

type ValidateTokenCommand struct {
	AccessToken string
}

type ValidateTokenResult struct {
	UserID     string
	Role       domain.Role
	CustomerID string
	ExpiresAt  time.Time
}

type ValidateTokenUseCase struct {
	verifier TokenVerifier
}

func NewValidateTokenUseCase(verifier TokenVerifier) *ValidateTokenUseCase {
	return &ValidateTokenUseCase{verifier: verifier}
}

func (u *ValidateTokenUseCase) ValidateToken(ctx context.Context, cmd ValidateTokenCommand) (ValidateTokenResult, error) {
	claims, err := u.verifier.Verify(ctx, cmd.AccessToken)
	if err != nil {
		return ValidateTokenResult{}, fmt.Errorf("validate token: %w", err)
	}

	role, err := singleRole(claims.Roles)
	if err != nil {
		return ValidateTokenResult{}, err
	}
	return ValidateTokenResult{
		UserID:     claims.Subject,
		Role:       role,
		CustomerID: claims.CustomerID,
		ExpiresAt:  claims.ExpiresAt,
	}, nil
}

func singleRole(roles []string) (domain.Role, error) {
	if len(roles) != 1 {
		return "", fmt.Errorf("validate token: expected exactly one role claim: %w", errs.ErrUnauthenticated)
	}
	role := domain.Role(roles[0])
	if !role.Valid() {
		return "", fmt.Errorf("validate token: unknown role %q: %w", roles[0], errs.ErrUnauthenticated)
	}
	return role, nil
}
