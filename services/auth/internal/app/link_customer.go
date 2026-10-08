package app

import (
	"context"
	"fmt"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type LinkCustomerCommand struct {
	EventID    string
	EventType  string
	UserID     string
	CustomerID string
}

type LinkCustomerUseCase struct {
	tx  domain.Transactor
	clk domain.Clock
}

func NewLinkCustomerUseCase(tx domain.Transactor, clk domain.Clock) *LinkCustomerUseCase {
	return &LinkCustomerUseCase{tx: tx, clk: clk}
}

func (u *LinkCustomerUseCase) LinkCustomer(ctx context.Context, cmd LinkCustomerCommand) error {
	if cmd.EventID == "" || cmd.EventType == "" || cmd.UserID == "" || cmd.CustomerID == "" {
		return fmt.Errorf("link customer: event_id, event_type, user_id and customer_id are required: %w", errs.ErrInvalidArgument)
	}

	at := u.clk.Now().UTC()
	return u.tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		inserted, err := uow.ProcessedEvents().MarkProcessed(ctx, cmd.EventID, cmd.EventType, at)
		if err != nil {
			return fmt.Errorf("link customer: mark %s processed: %w", cmd.EventID, err)
		}
		if !inserted {
			return nil
		}
		if err := uow.Users().SetCustomerID(ctx, cmd.UserID, cmd.CustomerID, at); err != nil {
			return fmt.Errorf("link customer: set customer id for %s: %w", cmd.UserID, err)
		}
		return nil
	})
}
