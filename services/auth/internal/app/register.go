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
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

const idempotencyScopeRegister = "auth.register"

type RegisterCommand struct {
	Email          string
	Password       string
	IdempotencyKey string
	Headers        map[string]string
}

type RegisterResult struct {
	UserID     string
	CustomerID string
	Role       domain.Role
}

type RegisterUseCase struct {
	tx      domain.Transactor
	hasher  domain.PasswordHasher
	clock   domain.Clock
	ids     domain.ID
	metrics domain.Metrics
}

func NewRegisterUseCase(tx domain.Transactor, hasher domain.PasswordHasher, clk domain.Clock, ids domain.ID, metrics domain.Metrics) *RegisterUseCase {
	return &RegisterUseCase{tx: tx, hasher: hasher, clock: clk, ids: ids, metrics: metrics}
}

func (u *RegisterUseCase) Register(ctx context.Context, cmd RegisterCommand) (RegisterResult, error) {
	email, err := validateCredentials(cmd.Email, cmd.Password, cmd.IdempotencyKey)
	if err != nil {
		return RegisterResult{}, err
	}

	account, created, err := createAccount(ctx, u.tx, u.hasher, u.ids, accountRequest{
		Scope:          idempotencyScopeRegister,
		IdempotencyKey: cmd.IdempotencyKey,
		RequestHash:    accountRequestHash(idempotencyScopeRegister, email),
		Email:          email,
		Password:       cmd.Password,
		Role:           domain.RoleClient,
		Headers:        cmd.Headers,
	}, u.clock.Now().UTC())
	if err != nil {
		return RegisterResult{}, err
	}
	if created && u.metrics != nil {
		u.metrics.RegistrationCreated(ctx)
	}
	return RegisterResult{UserID: account.UserID, Role: account.Role}, nil
}

type accountRequest struct {
	Scope          string
	IdempotencyKey string
	RequestHash    []byte
	Email          string
	Password       string
	Role           domain.Role
	Headers        map[string]string
}

type accountResult struct {
	UserID string
	Role   domain.Role
}

func createAccount(ctx context.Context, tx domain.Transactor, hasher domain.PasswordHasher, ids domain.ID, req accountRequest, at time.Time) (accountResult, bool, error) {
	var result accountResult
	created := false

	err := tx.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		record, err := uow.Idempotency().Get(ctx, req.Scope, req.IdempotencyKey)
		switch {
		case err == nil:
			if !bytes.Equal(record.RequestHash, req.RequestHash) {
				return fmt.Errorf("account: idempotency key reused with a different payload: %w", errs.ErrConflict)
			}
			replayed, decErr := decodeAccountResult(record.Response)
			if decErr != nil {
				return fmt.Errorf("account: decode stored response: %w", decErr)
			}
			result = replayed
			return nil
		case errors.Is(err, errs.ErrNotFound):
			// First execution for this key.
		default:
			return fmt.Errorf("account: idempotency lookup: %w", err)
		}

		hash, err := hasher.Hash(req.Password)
		if err != nil {
			return fmt.Errorf("account: hash password: %w", err)
		}
		user := domain.User{
			ID:           ids.New(),
			Email:        req.Email,
			PasswordHash: hash,
			Role:         req.Role,
			CreatedAt:    at,
			UpdatedAt:    at,
		}
		if err := uow.Users().Create(ctx, user); err != nil {
			return fmt.Errorf("account: create user: %w", err)
		}
		if err := enqueueUserRegistered(ctx, uow, user, ids.New(), at, req.Headers); err != nil {
			return err
		}

		result = accountResult{UserID: user.ID, Role: user.Role}
		created = true
		response, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("account: encode response: %w", err)
		}
		if err := uow.Idempotency().Put(ctx, domain.IdempotencyRecord{
			Scope:       req.Scope,
			Key:         req.IdempotencyKey,
			RequestHash: req.RequestHash,
			Response:    response,
			CreatedAt:   at,
		}); err != nil {
			return fmt.Errorf("account: store idempotency key: %w", err)
		}
		return nil
	})
	if err != nil {
		return accountResult{}, false, err
	}
	return result, created, nil
}

func validateCredentials(rawEmail, password, idempotencyKey string) (string, error) {
	email := domain.NormalizeEmail(rawEmail)
	if err := domain.ValidateEmail(email); err != nil {
		return "", fmt.Errorf("account: invalid email: %w", errs.ErrInvalidArgument)
	}
	if err := domain.ValidatePassword(password); err != nil {
		return "", fmt.Errorf("account: invalid password: %w", errs.ErrInvalidArgument)
	}
	if idempotencyKey == "" {
		return "", fmt.Errorf("account: idempotency key is required: %w", errs.ErrInvalidArgument)
	}
	return email, nil
}

func enqueueUserRegistered(ctx context.Context, uow domain.UnitOfWork, user domain.User, eventID string, at time.Time, headers map[string]string) error {
	envelope, err := events.Envelope(eventID, events.TypeUserRegistered, at, &eventsv1.UserRegistered{
		UserId: user.ID,
		Email:  user.Email,
		Role:   string(user.Role),
	})
	if err != nil {
		return fmt.Errorf("account: build user_registered event: %w", err)
	}
	payload, err := events.Encode(envelope)
	if err != nil {
		return fmt.Errorf("account: encode user_registered event: %w", err)
	}
	if err := uow.Outbox().Enqueue(ctx, domain.OutboxMessage{
		ID:         eventID,
		Topic:      events.TopicAuth,
		EventType:  events.TypeUserRegistered,
		Headers:    headers,
		OccurredAt: at,
		Payload:    payload,
	}); err != nil {
		return fmt.Errorf("account: enqueue user_registered event: %w", err)
	}
	return nil
}

func accountRequestHash(scope, email string) []byte {
	sum := sha256.Sum256([]byte(scope + "\n" + email))
	return sum[:]
}

func decodeAccountResult(data []byte) (accountResult, error) {
	var result accountResult
	if err := json.Unmarshal(data, &result); err != nil {
		return accountResult{}, err
	}
	return result, nil
}
