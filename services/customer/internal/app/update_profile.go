package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	eventsv1 "github.com/zima-flow/go-mentor/mini-fintech/contracts/gen/bank/events/v1"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

const idempotencyScopeUpdateProfile = "customer.update_profile"

type UpdateProfileCommand struct {
	UserID         string
	FullName       string
	DateOfBirth    string
	Address        string
	Phone          string
	Citizenship    string
	IdempotencyKey string
	Headers        map[string]string
}

type UpdateProfileResult struct {
	Customer domain.Customer
}

type UpdateProfileUseCase struct {
	tx      domain.Transactor
	clock   domain.Clock
	ids     domain.ID
	metrics domain.Metrics
}

func NewUpdateProfileUseCase(tx domain.Transactor, clk domain.Clock, ids domain.ID, metrics domain.Metrics) *UpdateProfileUseCase {
	return &UpdateProfileUseCase{tx: tx, clock: clk, ids: ids, metrics: metrics}
}

func (u *UpdateProfileUseCase) UpdateProfile(ctx context.Context, cmd UpdateProfileCommand) (UpdateProfileResult, error) {
	if cmd.UserID == "" {
		return UpdateProfileResult{}, fmt.Errorf("update profile: user_id is required: %w", errs.ErrInvalidArgument)
	}
	if cmd.IdempotencyKey == "" {
		return UpdateProfileResult{}, fmt.Errorf("update profile: idempotency key is required: %w", errs.ErrInvalidArgument)
	}

	at := u.clock.Now().UTC()
	fields, err := domain.ValidateProfile(domain.ProfileFields{
		FullName:    cmd.FullName,
		DateOfBirth: cmd.DateOfBirth,
		Address:     cmd.Address,
		Phone:       cmd.Phone,
		Citizenship: cmd.Citizenship,
	}, at)
	if err != nil {
		return UpdateProfileResult{}, fmt.Errorf("update profile: %w", errs.ErrInvalidArgument)
	}

	var result UpdateProfileResult
	filled := false
	err = u.tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		customer, err := uow.Customers().ByUserID(ctx, cmd.UserID)
		if err != nil {
			return fmt.Errorf("update profile: load profile for %s: %w", cmd.UserID, err)
		}

		scope := idempotencyScopeUpdateProfile + ":" + customer.ID
		requestHash := updateProfileRequestHash(scope, fields)

		record, err := uow.Idempotency().Get(ctx, scope, cmd.IdempotencyKey)
		switch {
		case err == nil:
			if !bytes.Equal(record.RequestHash, requestHash) {
				return fmt.Errorf("update profile: idempotency key reused with a different payload: %w", errs.ErrConflict)
			}
			replayed, decErr := decodeUpdateProfileResult(record.Response)
			if decErr != nil {
				return fmt.Errorf("update profile: decode stored response: %w", decErr)
			}
			result = replayed
			return nil
		case errors.Is(err, errs.ErrNotFound):
		default:
			return fmt.Errorf("update profile: idempotency lookup: %w", err)
		}

		if !domain.CanEditProfile(customer.Status) {
			return fmt.Errorf("update profile: profile in status %q is locked: %w", customer.Status, errs.ErrFailedPrecondition)
		}

		emitFilled := false
		if customer.Status == domain.StatusNew {
			if err := domain.ApplyTransition(customer.Status, domain.StatusProfileFilled); err != nil {
				return fmt.Errorf("update profile: %w", errs.ErrFailedPrecondition)
			}
			customer.Status = domain.StatusProfileFilled
			emitFilled = true
			filled = true
		}

		customer.FullName = fields.FullName
		customer.DateOfBirth = fields.DateOfBirth
		customer.Address = fields.Address
		customer.Phone = fields.Phone
		customer.Citizenship = fields.Citizenship
		customer.UpdatedAt = at

		if err := uow.Customers().Update(ctx, customer); err != nil {
			return fmt.Errorf("update profile: persist profile %s: %w", customer.ID, err)
		}
		if emitFilled {
			if err := enqueueProfileFilled(ctx, uow, customer, u.ids.New(), at, cmd.Headers); err != nil {
				return err
			}
		}

		result = UpdateProfileResult{Customer: customer}
		response, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("update profile: encode response: %w", err)
		}
		if err := uow.Idempotency().Put(ctx, domain.IdempotencyRecord{
			Scope:       scope,
			Key:         cmd.IdempotencyKey,
			RequestHash: requestHash,
			Response:    response,
			CreatedAt:   at,
		}); err != nil {
			return fmt.Errorf("update profile: store idempotency key: %w", err)
		}
		return nil
	})
	if err != nil {
		return UpdateProfileResult{}, err
	}
	if filled && u.metrics != nil {
		u.metrics.ProfileFilled(ctx)
	}
	return result, nil
}

func updateProfileRequestHash(scope string, p domain.ProfileFields) []byte {
	h := sha256.New()
	for _, part := range []string{scope, p.FullName, p.DateOfBirth, p.Address, p.Phone, p.Citizenship} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

func decodeUpdateProfileResult(data []byte) (UpdateProfileResult, error) {
	var result UpdateProfileResult
	if err := json.Unmarshal(data, &result); err != nil {
		return UpdateProfileResult{}, err
	}
	return result, nil
}

func enqueueProfileFilled(ctx context.Context, uow domain.UnitOfWork, customer domain.Customer, eventID string, at time.Time, headers map[string]string) error {
	envelope, err := events.Envelope(eventID, events.TypeProfileFilled, at, &eventsv1.ProfileFilled{
		CustomerId: customer.ID,
		UserId:     customer.UserID,
	})
	if err != nil {
		return fmt.Errorf("update profile: build profile_filled event: %w", err)
	}
	payload, err := events.Encode(envelope)
	if err != nil {
		return fmt.Errorf("update profile: encode profile_filled event: %w", err)
	}
	if err := uow.Outbox().Enqueue(ctx, domain.OutboxMessage{
		ID:         eventID,
		Topic:      events.TopicCustomer,
		EventType:  events.TypeProfileFilled,
		Headers:    headers,
		OccurredAt: at,
		Payload:    payload,
	}); err != nil {
		return fmt.Errorf("update profile: enqueue profile_filled event: %w", err)
	}
	return nil
}
