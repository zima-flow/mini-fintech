package domain

import (
	"context"
	"time"
)

type Clock interface {
	Now() time.Time
}

type ID interface {
	New() string
}

type UserRepo interface {
	Create(ctx context.Context, user User) error
	ByEmail(ctx context.Context, email string) (User, error)
	ByID(ctx context.Context, id string) (User, error)
	SetCustomerID(ctx context.Context, userID, customerID string, at time.Time) error
}

type RefreshTokenRepo interface {
	Create(ctx context.Context, token RefreshToken) error
	ByHashForUpdate(ctx context.Context, tokenHash []byte) (RefreshToken, error)
	MarkRotated(ctx context.Context, id, successorID string, at time.Time) error
	RevokeFamily(ctx context.Context, familyID string, at time.Time, reason string) error
}

type IdempotencyRecord struct {
	Scope       string
	Key         string
	RequestHash []byte
	Response    []byte
	CreatedAt   time.Time
}

type IdempotencyRepo interface {
	Get(ctx context.Context, scope, key string) (IdempotencyRecord, error)
	Put(ctx context.Context, record IdempotencyRecord) error
}

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, encodedHash string) (bool, error)
}

type TokenIssuer interface {
	IssueAccess(ctx context.Context, claims AccessClaims) (string, error)
}

type OutboxMessage struct {
	ID         string
	Topic      string
	EventType  string
	OccurredAt time.Time
	Headers    map[string]string
	Payload    []byte
}

type Outbox interface {
	Enqueue(ctx context.Context, message OutboxMessage) error
}

type Metrics interface {
	RegistrationCreated(ctx context.Context)
}

type ProcessedEventsRepo interface {
	MarkProcessed(ctx context.Context, eventID, eventType string, at time.Time) (bool, error)
}

type UnitOfWork interface {
	Users() UserRepo
	RefreshTokens() RefreshTokenRepo
	Idempotency() IdempotencyRepo
	Outbox() Outbox
	ProcessedEvents() ProcessedEventsRepo
}

type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, uow UnitOfWork) error) error
}
