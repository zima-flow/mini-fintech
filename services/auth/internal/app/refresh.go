package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type RefreshCommand struct {
	RefreshToken string
}

type RefreshResult struct {
	Tokens domain.TokenPair
}

type RefreshUseCase struct {
	tx     domain.Transactor
	minter tokenMinter
}

func NewRefreshUseCase(tx domain.Transactor, issuer domain.TokenIssuer, clk domain.Clock, ids domain.ID, ttls TokenTTLs) *RefreshUseCase {
	return &RefreshUseCase{
		tx:     tx,
		minter: tokenMinter{issuer: issuer, clock: clk, ids: ids, ttls: ttls},
	}
}

func (u *RefreshUseCase) Refresh(ctx context.Context, cmd RefreshCommand) (RefreshResult, error) {
	var (
		result  RefreshResult
		authErr error
	)

	err := u.tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		now := u.minter.now()
		existing, err := uow.RefreshTokens().ByHashForUpdate(ctx, domain.HashRefreshToken(cmd.RefreshToken))
		if err != nil {
			if errors.Is(err, errs.ErrNotFound) {
				return errInvalidRefreshToken()
			}
			return fmt.Errorf("refresh: load token: %w", err)
		}

		if !existing.Reusable(now) {
			if existing.Rotated() || existing.Revoked() {
				if err := uow.RefreshTokens().RevokeFamily(ctx, existing.FamilyID, now, "reuse detected"); err != nil {
					return fmt.Errorf("refresh: revoke family: %w", err)
				}
			}
			authErr = errInvalidRefreshToken()
			return nil
		}

		user, err := uow.Users().ByID(ctx, existing.UserID)
		if err != nil {
			if errors.Is(err, errs.ErrNotFound) {
				return errInvalidRefreshToken()
			}
			return fmt.Errorf("refresh: load user: %w", err)
		}

		pair, successorID, err := u.minter.mint(ctx, uow, user, existing.FamilyID, now)
		if err != nil {
			return fmt.Errorf("refresh: %w", err)
		}
		if err := uow.RefreshTokens().MarkRotated(ctx, existing.ID, successorID, now); err != nil {
			return fmt.Errorf("refresh: mark rotated: %w", err)
		}
		result = RefreshResult{Tokens: pair}
		return nil
	})
	if err != nil {
		return RefreshResult{}, err
	}
	if authErr != nil {
		return RefreshResult{}, authErr
	}
	return result, nil
}

func errInvalidRefreshToken() error {
	return fmt.Errorf("refresh: invalid refresh token: %w", errs.ErrUnauthenticated)
}
