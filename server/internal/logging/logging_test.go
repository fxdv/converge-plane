package logging

import (
	"context"
	"log/slog"
	"testing"
)

// TestNewLevelMapping pins the documented contract: the level names map to
// slog levels, case-insensitively, and anything unknown falls back to info
// (a deployment misspelling its log level must log, not drop).
func TestNewLevelMapping(t *testing.T) {
	cases := []struct {
		in   string
		want struct {
			debug, info, warn, error bool
		}
	}{
		{"debug", struct{ debug, info, warn, error bool }{true, true, true, true}},
		{"info", struct{ debug, info, warn, error bool }{false, true, true, true}},
		{"warn", struct{ debug, info, warn, error bool }{false, false, true, true}},
		{"error", struct{ debug, info, warn, error bool }{false, false, false, true}},
		{"DEBUG", struct{ debug, info, warn, error bool }{true, true, true, true}},
		{"Error", struct{ debug, info, warn, error bool }{false, false, false, true}},
		{"bogus", struct{ debug, info, warn, error bool }{false, true, true, true}},
		{"", struct{ debug, info, warn, error bool }{false, true, true, true}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			l := New(c.in)
			if l.Enabled(context.Background(), slog.LevelDebug) != c.want.debug {
				t.Errorf("debug enabled = %v, want %v", l.Enabled(context.Background(), slog.LevelDebug), c.want.debug)
			}
			if l.Enabled(context.Background(), slog.LevelInfo) != c.want.info {
				t.Errorf("info enabled = %v, want %v", l.Enabled(context.Background(), slog.LevelInfo), c.want.info)
			}
			if l.Enabled(context.Background(), slog.LevelWarn) != c.want.warn {
				t.Errorf("warn enabled = %v, want %v", l.Enabled(context.Background(), slog.LevelWarn), c.want.warn)
			}
			if l.Enabled(context.Background(), slog.LevelError) != c.want.error {
				t.Errorf("error enabled = %v, want %v", l.Enabled(context.Background(), slog.LevelError), c.want.error)
			}
			if l == nil {
				t.Fatal("New returned nil")
			}
		})
	}
}
