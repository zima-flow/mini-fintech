package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/app"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/domain"
)

type fakeEchoer struct {
	calls []domain.Echo
}

func (f *fakeEchoer) Echo(_ context.Context, message string, at time.Time) (domain.Echo, error) {
	echo := domain.Echo{ID: "fixed-id", Message: message, At: at}
	f.calls = append(f.calls, echo)
	return echo, nil
}

func TestUseCaseEcho_TrimsAndRecordsFixedTime(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	repo := &fakeEchoer{}
	uc := app.NewUseCase(repo, clock.Fixed{T: fixed})

	got, err := uc.Echo(context.Background(), "  hi  ")
	require.NoError(t, err)
	require.Equal(t, "hi", got.Message)
	require.Equal(t, fixed, got.At)
	require.Len(t, repo.calls, 1, "the use case must persist exactly one call")
	require.Equal(t, "hi", repo.calls[0].Message)
	require.Equal(t, fixed, repo.calls[0].At, "time must come only from the injected Clock")
}

func TestUseCaseEcho_EmptyIsInvalidArgument(t *testing.T) {
	t.Parallel()

	repo := &fakeEchoer{}
	uc := app.NewUseCase(repo, clock.Fixed{T: time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)})

	_, err := uc.Echo(context.Background(), "   ")
	require.ErrorIs(t, err, errs.ErrInvalidArgument)
	require.Empty(t, repo.calls, "an invalid message must not reach the repository")
}
