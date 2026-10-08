//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/events"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/id"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/postgres"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/domain"
)

type plainHasher struct{}

func (plainHasher) Hash(password string) (string, error) { return "hash:" + password, nil }

func (plainHasher) Verify(password, hash string) (bool, error) { return hash == "hash:"+password, nil }

type fixedIssuer struct{}

func (fixedIssuer) IssueAccess(_ context.Context, claims domain.AccessClaims) (string, error) {
	return "access:" + claims.UserID + ":" + claims.TokenID, nil
}

func testTTLs() app.TokenTTLs {
	return app.TokenTTLs{Access: 15 * time.Minute, Refresh: 30 * 24 * time.Hour}
}

const testPassword = "correct horse battery staple"

func TestRegister_PersistsUserAndEventOverOneTransaction(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	uc := app.NewRegisterUseCase(postgres.NewTransactor(pool), plainHasher{}, clock.Fixed{T: testNow()}, id.UUIDv7{}, nil)

	result, err := uc.Register(ctx, app.RegisterCommand{
		Email:          "  User@Example.COM ",
		Password:       testPassword,
		IdempotencyKey: "reg-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.UserID)
	require.Equal(t, domain.RoleClient, result.Role)

	var email, role string
	require.NoError(t, pool.QueryRow(ctx, `SELECT email, role FROM users WHERE id = $1`, result.UserID).Scan(&email, &role))
	require.Equal(t, "user@example.com", email, "the email is normalized")
	require.Equal(t, "CLIENT", role)

	var payload []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT payload FROM outbox WHERE event_type = $1`, events.TypeUserRegistered).Scan(&payload))
	envelope, err := events.Decode(payload)
	require.NoError(t, err)
	require.Equal(t, events.TypeUserRegistered, envelope.GetEventType())
	require.Equal(t, result.UserID, envelope.GetUserRegistered().GetUserId())

	var idempotency int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM idempotency_keys WHERE scope = 'auth.register' AND key = 'reg-1'`,
	).Scan(&idempotency))
	require.Equal(t, 1, idempotency)
}

func TestRegister_ReplayIsNoOp(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	uc := app.NewRegisterUseCase(postgres.NewTransactor(pool), plainHasher{}, clock.Fixed{T: testNow()}, id.UUIDv7{}, nil)
	cmd := app.RegisterCommand{Email: "user@example.com", Password: testPassword, IdempotencyKey: "reg-1"}

	first, err := uc.Register(ctx, cmd)
	require.NoError(t, err)
	second, err := uc.Register(ctx, cmd)
	require.NoError(t, err)
	require.Equal(t, first.UserID, second.UserID)

	var users, outbox int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&outbox))
	require.Equal(t, 1, users)
	require.Equal(t, 1, outbox, "a replay must not enqueue a second event")
}

func TestRegister_RollsBackUserAndEventOnFailure(t *testing.T) {
	ctx := context.Background()
	pool := startAuthPostgres(t, ctx)
	uc := app.NewRegisterUseCase(failingPutTransactor{inner: postgres.NewTransactor(pool)}, plainHasher{}, clock.Fixed{T: testNow()}, id.UUIDv7{}, nil)

	_, err := uc.Register(ctx, app.RegisterCommand{
		Email:          "rollback@example.com",
		Password:       testPassword,
		IdempotencyKey: "reg-rb",
	})
	require.Error(t, err)

	var users, outbox int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = 'rollback@example.com'`).Scan(&users))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&outbox))
	require.Zero(t, users, "a failed registration must not leave a user")
	require.Zero(t, outbox, "a failed registration must not leave an event")
}

func TestRefresh_ConcurrentReuse_OneWinner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := startAuthPostgres(t, ctx)
	tx := postgres.NewTransactor(pool)
	clk := clock.Fixed{T: testNow()}
	ids := id.UUIDv7{}
	issuer := fixedIssuer{}

	register := app.NewRegisterUseCase(tx, plainHasher{}, clk, ids, nil)
	_, err := register.Register(ctx, app.RegisterCommand{Email: "client@example.com", Password: testPassword, IdempotencyKey: "reg-c"})
	require.NoError(t, err)

	login := app.NewLoginUseCase(tx, plainHasher{}, issuer, clk, ids, testTTLs())
	loggedIn, err := login.Login(ctx, app.LoginCommand{Email: "client@example.com", Password: testPassword})
	require.NoError(t, err)
	secret := loggedIn.Tokens.RefreshToken
	require.NotEmpty(t, secret)

	refresh := app.NewRefreshUseCase(tx, issuer, clk, ids, testTTLs())

	start := make(chan struct{})
	results := make([]app.RefreshResult, 2)
	errsSeen := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errsSeen[i] = refresh.Refresh(ctx, app.RefreshCommand{RefreshToken: secret})
		}()
	}
	close(start)
	wg.Wait()

	successes := 0
	var winner app.RefreshResult
	for i, refreshErr := range errsSeen {
		if refreshErr == nil {
			successes++
			winner = results[i]
			continue
		}
		require.ErrorIs(t, refreshErr, errs.ErrUnauthenticated)
	}
	require.Equal(t, 1, successes, "exactly one concurrent refresh wins")

	var total, revoked int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*), count(revoked_at) FROM refresh_tokens WHERE user_id = $1`, loggedIn.UserID,
	).Scan(&total, &revoked))
	require.Equal(t, 2, total, "the family holds the original and its successor")
	require.Equal(t, 2, revoked, "reuse detection revokes the whole family")

	_, err = refresh.Refresh(ctx, app.RefreshCommand{RefreshToken: winner.Tokens.RefreshToken})
	require.ErrorIs(t, err, errs.ErrUnauthenticated, "the successor is revoked with the family")
}

type failingPutTransactor struct{ inner domain.Transactor }

func (t failingPutTransactor) WithinTx(ctx context.Context, fn func(context.Context, domain.UnitOfWork) error) error {
	return t.inner.WithinTx(ctx, func(ctx context.Context, uow domain.UnitOfWork) error {
		return fn(ctx, failingPutUnitOfWork{UnitOfWork: uow})
	})
}

type failingPutUnitOfWork struct{ domain.UnitOfWork }

func (u failingPutUnitOfWork) Idempotency() domain.IdempotencyRepo {
	return failingPutIdempotency{IdempotencyRepo: u.UnitOfWork.Idempotency()}
}

type failingPutIdempotency struct{ domain.IdempotencyRepo }

func (failingPutIdempotency) Put(context.Context, domain.IdempotencyRecord) error {
	return errors.New("forced idempotency failure")
}
