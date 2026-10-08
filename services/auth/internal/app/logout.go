package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type LogoutCommand struct {
	RefreshToken string
}

type LogoutUseCase struct {
	tx    domain.Transactor
	clock domain.Clock
}

func NewLogoutUseCase(tx domain.Transactor, clk domain.Clock) *LogoutUseCase {
	return &LogoutUseCase{tx: tx, clock: clk}
}

func (u *LogoutUseCase) Logout(ctx context.Context, cmd LogoutCommand) error {
	return u.tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		existing, err := uow.RefreshTokens().ByHashForUpdate(ctx, domain.HashRefreshToken(cmd.RefreshToken))
		if err != nil {
			if errors.Is(err, errs.ErrNotFound) {
				return errInvalidRefreshToken()
			}
			return fmt.Errorf("logout: load token: %w", err)
		}
		if err := uow.RefreshTokens().RevokeFamily(ctx, existing.FamilyID, u.clock.Now().UTC(), "logout"); err != nil {
			return fmt.Errorf("logout: revoke family: %w", err)
		}
		return nil
	})
}
