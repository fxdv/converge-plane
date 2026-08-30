// Package logging provides the structured logger used across the server.
// Logs are emitted as JSON on stderr so that any container runtime or
// aggregator can consume them without extra tooling.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New builds a JSON slog.Logger for the given level name
// (debug, info, warn, error). Unknown names fall back to info.
func New(level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l})
	return slog.New(handler)
}
