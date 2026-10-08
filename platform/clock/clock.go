package clock

import "time"

type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

type Fixed struct {
	T time.Time
}

func (f Fixed) Now() time.Time { return f.T.UTC() }

func (Fixed) After(time.Duration) <-chan time.Time { return make(chan time.Time) }
