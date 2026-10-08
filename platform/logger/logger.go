package logger

import (
	"io"
	"log/slog"
	"os"
)

func New(level string, service string) *slog.Logger {
	return newLogger(os.Stdout, level, service)
}

func newLogger(w io.Writer, level string, service string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if lvl == slog.LevelDebug {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}
	return slog.New(handler).With(slog.String("service", service))
}
