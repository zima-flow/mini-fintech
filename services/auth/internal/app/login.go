package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type TokenTTLs struct {
	Access  time.Duration
	Refresh time.Duration
}

type LoginCommand struct {
	Email    string
	Password string
}

type LoginResult struct {
	Tokens     domain.TokenPair
	UserID     string
	Role       domain.Role
	CustomerID string
}

type LoginUseCase struct {
	tx     domain.Transactor
	hasher domain.PasswordHasher
	minter tokenMinter
}

func NewLoginUseCase(tx domain.Transactor, hasher domain.PasswordHasher, issuer domain.TokenIssuer, clk domain.Clock, ids domain.ID, ttls TokenTTLs) *LoginUseCase {
	return &LoginUseCase{
		tx:     tx,
		hasher: hasher,
		minter: tokenMinter{issuer: issuer, clock: clk, ids: ids, ttls: ttls},
	}
}

func (u *LoginUseCase) Login(ctx context.Context, cmd LoginCommand) (LoginResult, error) {
	email := domain.NormalizeEmail(cmd.Email)

	var result LoginResult
	err := u.tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		user, err := uow.Users().ByEmail(ctx, email)
		if err != nil {
			if errors.Is(err, errs.ErrNotFound) {
				return errInvalidCredentials()
			}
			return fmt.Errorf("login: load user: %w", err)
		}

		ok, err := u.hasher.Verify(cmd.Password, user.PasswordHash)
		if err != nil {
			return fmt.Errorf("login: verify password: %w", err)
		}
		if !ok {
			return errInvalidCredentials()
		}

		pair, _, err := u.minter.mint(ctx, uow, user, u.minter.newFamilyID(), u.minter.now())
		if err != nil {
			return fmt.Errorf("login: %w", err)
		}
		result = LoginResult{
			Tokens:     pair,
			UserID:     user.ID,
			Role:       user.Role,
			CustomerID: user.CustomerID,
		}
		return nil
	})
	if err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

func errInvalidCredentials() error {
	return fmt.Errorf("login: invalid email or password: %w", errs.ErrUnauthenticated)
}
