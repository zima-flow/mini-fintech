package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/customer/internal/domain"
)

type Transactor struct {
	inner *platformpostgres.Transactor
}

var _ domain.Transactor = (*Transactor)(nil)

func NewTransactor(pool *pgxpool.Pool) *Transactor {
	return &Transactor{inner: platformpostgres.NewTransactor(pool)}
}

func (t *Transactor) WithinTx(ctx context.Context, fn func(ctx context.Context, uow domain.UnitOfWork) error) error {
	return t.inner.WithinTx(ctx, func(ctx context.Context, db platformpostgres.DBTX) error {
		return fn(ctx, newUnitOfWork(db))
	})
}

type unitOfWork struct {
	customers       domain.CustomerRepo
	idempotency     domain.IdempotencyRepo
	outbox          domain.Outbox
	processedEvents domain.ProcessedEventsRepo
}

var _ domain.UnitOfWork = (*unitOfWork)(nil)

func newUnitOfWork(db platformpostgres.DBTX) *unitOfWork {
	return &unitOfWork{
		customers:       NewCustomerRepo(db),
		idempotency:     NewIdempotencyRepo(db),
		outbox:          NewOutbox(db),
		processedEvents: NewProcessedEventsRepo(db),
	}
}

func (u *unitOfWork) Customers() domain.CustomerRepo              { return u.customers }
func (u *unitOfWork) Idempotency() domain.IdempotencyRepo         { return u.idempotency }
func (u *unitOfWork) Outbox() domain.Outbox                       { return u.outbox }
func (u *unitOfWork) ProcessedEvents() domain.ProcessedEventsRepo { return u.processedEvents }
