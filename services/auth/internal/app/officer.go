package app

import (
	"context"
	"fmt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

const idempotencyScopeCreateOfficer = "auth.create_officer"

type CreateOfficerCommand struct {
	Email          string
	Password       string
	IdempotencyKey string
	ActorRole      domain.Role
	Headers        map[string]string
}

type CreateOfficerResult struct {
	UserID string
	Role   domain.Role
}

type CreateOfficerUseCase struct {
	tx     domain.Transactor
	hasher domain.PasswordHasher
	clock  domain.Clock
	ids    domain.ID
}

func NewCreateOfficerUseCase(tx domain.Transactor, hasher domain.PasswordHasher, clk domain.Clock, ids domain.ID) *CreateOfficerUseCase {
	return &CreateOfficerUseCase{tx: tx, hasher: hasher, clock: clk, ids: ids}
}

func (u *CreateOfficerUseCase) CreateOfficer(ctx context.Context, cmd CreateOfficerCommand) (CreateOfficerResult, error) {
	if cmd.ActorRole != domain.RoleAdmin {
		return CreateOfficerResult{}, fmt.Errorf("create officer: caller role %q is not ADMIN: %w", cmd.ActorRole, errs.ErrPermissionDenied)
	}

	email, err := validateCredentials(cmd.Email, cmd.Password, cmd.IdempotencyKey)
	if err != nil {
		return CreateOfficerResult{}, err
	}

	account, _, err := createAccount(ctx, u.tx, u.hasher, u.ids, accountRequest{
		Scope:          idempotencyScopeCreateOfficer,
		IdempotencyKey: cmd.IdempotencyKey,
		RequestHash:    accountRequestHash(idempotencyScopeCreateOfficer, email),
		Email:          email,
		Password:       cmd.Password,
		Role:           domain.RoleOfficer,
		Headers:        cmd.Headers,
	}, u.clock.Now().UTC())
	if err != nil {
		return CreateOfficerResult{}, err
	}
	return CreateOfficerResult(account), nil
}
