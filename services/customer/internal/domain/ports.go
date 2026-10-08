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

type CustomerFilter struct {
	Status CustomerStatus
	Limit  int
	Offset int
}

type CustomerRepo interface {
	Create(ctx context.Context, customer Customer) error
	ByUserID(ctx context.Context, userID string) (Customer, error)
	ByID(ctx context.Context, customerID string) (Customer, error)
	Update(ctx context.Context, customer Customer) error
	List(ctx context.Context, filter CustomerFilter) ([]Customer, error)
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
	ProfileCreated(ctx context.Context)
	ProfileFilled(ctx context.Context)
}

type ProcessedEventsRepo interface {
	MarkProcessed(ctx context.Context, eventID, eventType string, at time.Time) (bool, error)
}

type UnitOfWork interface {
	Customers() CustomerRepo
	Idempotency() IdempotencyRepo
	Outbox() Outbox
	ProcessedEvents() ProcessedEventsRepo
}

type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, uow UnitOfWork) error) error
}
