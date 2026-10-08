package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	platformpostgres "github.com/zima-flow/go-mentor/mini-fintech/platform/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
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
	users           domain.UserRepo
	refresh         domain.RefreshTokenRepo
	idempotency     domain.IdempotencyRepo
	outbox          domain.Outbox
	processedEvents domain.ProcessedEventsRepo
}

var _ domain.UnitOfWork = (*unitOfWork)(nil)

func newUnitOfWork(db platformpostgres.DBTX) *unitOfWork {
	return &unitOfWork{
		users:           NewUserRepo(db),
		refresh:         NewRefreshTokenRepo(db),
		idempotency:     NewIdempotencyRepo(db),
		outbox:          NewOutbox(db),
		processedEvents: NewProcessedEventsRepo(db),
	}
}

func (u *unitOfWork) Users() domain.UserRepo                 { return u.users }
func (u *unitOfWork) RefreshTokens() domain.RefreshTokenRepo { return u.refresh }
func (u *unitOfWork) Idempotency() domain.IdempotencyRepo    { return u.idempotency }
func (u *unitOfWork) Outbox() domain.Outbox                  { return u.outbox }
func (u *unitOfWork) ProcessedEvents() domain.ProcessedEventsRepo {
	return u.processedEvents
}
