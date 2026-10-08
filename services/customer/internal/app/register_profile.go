package app

import (
	"context"
	"fmt"
	"time"

	eventsv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/events/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

type RegisterProfileCommand struct {
	EventID   string
	EventType string
	UserID    string
	Headers   map[string]string
}

type RegisterProfileUseCase struct {
	tx      domain.Transactor
	clock   domain.Clock
	ids     domain.ID
	metrics domain.Metrics
}

func NewRegisterProfileUseCase(tx domain.Transactor, clk domain.Clock, ids domain.ID, metrics domain.Metrics) *RegisterProfileUseCase {
	return &RegisterProfileUseCase{tx: tx, clock: clk, ids: ids, metrics: metrics}
}

func (u *RegisterProfileUseCase) RegisterProfile(ctx context.Context, cmd RegisterProfileCommand) error {
	if cmd.EventID == "" || cmd.EventType == "" || cmd.UserID == "" {
		return fmt.Errorf("register profile: event_id, event_type and user_id are required: %w", errs.ErrInvalidArgument)
	}

	at := u.clock.Now().UTC()
	created := false
	err := u.tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		inserted, err := uow.ProcessedEvents().MarkProcessed(ctx, cmd.EventID, cmd.EventType, at)
		if err != nil {
			return fmt.Errorf("register profile: mark %s processed: %w", cmd.EventID, err)
		}
		if !inserted {
			return nil
		}

		customer := domain.Customer{
			ID:        u.ids.New(),
			UserID:    cmd.UserID,
			Status:    domain.StatusNew,
			CreatedAt: at,
			UpdatedAt: at,
		}
		if err := uow.Customers().Create(ctx, customer); err != nil {
			return fmt.Errorf("register profile: create profile for %s: %w", cmd.UserID, err)
		}
		if err := enqueueProfileCreated(ctx, uow, customer, u.ids.New(), at, cmd.Headers); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return err
	}
	if created && u.metrics != nil {
		u.metrics.ProfileCreated(ctx)
	}
	return nil
}

func enqueueProfileCreated(ctx context.Context, uow domain.UnitOfWork, customer domain.Customer, eventID string, at time.Time, headers map[string]string) error {
	envelope, err := events.Envelope(eventID, events.TypeProfileCreated, at, &eventsv1.ProfileCreated{
		UserId:     customer.UserID,
		CustomerId: customer.ID,
	})
	if err != nil {
		return fmt.Errorf("register profile: build profile_created event: %w", err)
	}
	payload, err := events.Encode(envelope)
	if err != nil {
		return fmt.Errorf("register profile: encode profile_created event: %w", err)
	}
	if err := uow.Outbox().Enqueue(ctx, domain.OutboxMessage{
		ID:         eventID,
		Topic:      events.TopicCustomer,
		EventType:  events.TypeProfileCreated,
		Headers:    headers,
		OccurredAt: at,
		Payload:    payload,
	}); err != nil {
		return fmt.Errorf("register profile: enqueue profile_created event: %w", err)
	}
	return nil
}
