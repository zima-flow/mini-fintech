package clock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/clock"
)

func TestRealAfterFiresAfterDelay(t *testing.T) {
	t.Parallel()

	select {
	case <-(clock.Real{}).After(5 * time.Millisecond):
	case <-time.After(time.Second):
		t.Fatal("Real.After did not fire")
	}
}

func TestFixedAfterNeverFires(t *testing.T) {
	t.Parallel()

	select {
	case <-(clock.Fixed{T: time.Now()}).After(time.Millisecond):
		t.Fatal("Fixed is frozen: After must not fire")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRealNowIsUTC(t *testing.T) {
	t.Parallel()
	require.Equal(t, time.UTC, (clock.Real{}).Now().Location())
}

func TestFixedReturnsInjectedInstantInUTC(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("MSK", 3*60*60)
	instant := time.Date(2026, time.September, 25, 12, 0, 0, 0, loc)

	got := (clock.Fixed{T: instant}).Now()

	require.Equal(t, instant.UTC(), got)
	require.Equal(t, time.UTC, got.Location())
}
