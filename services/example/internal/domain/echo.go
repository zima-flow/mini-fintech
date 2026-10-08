package domain

import (
	"context"
	"time"
)

type Echo struct {
	ID      string
	Message string
	At      time.Time
}

type Echoer interface {
	Echo(ctx context.Context, message string, at time.Time) (Echo, error)
}
