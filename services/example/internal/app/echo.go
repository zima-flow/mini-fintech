package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
	"github.com/zima-flow/go-mentor/mini-fintech/platform/errs"
	"github.com/zima-flow/go-mentor/mini-fintech/services/example/internal/domain"
)

type UseCase struct {
	echoes domain.Echoer
	clock  clock.Clock
}

func NewUseCase(echoes domain.Echoer, clk clock.Clock) *UseCase {
	return &UseCase{echoes: echoes, clock: clk}
}

func (u *UseCase) Echo(ctx context.Context, message string) (domain.Echo, error) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return domain.Echo{}, fmt.Errorf("echo: empty message: %w", errs.ErrInvalidArgument)
	}

	echo, err := u.echoes.Echo(ctx, trimmed, u.clock.Now())
	if err != nil {
		return domain.Echo{}, fmt.Errorf("echo %q: %w", trimmed, err)
	}
	return echo, nil
}
