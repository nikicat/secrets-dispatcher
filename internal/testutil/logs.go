package testutil

import (
	"bytes"
	"log/slog"
	"testing"
)

// Sends the default logger's records at the given level and above to the
// returned buffer, as text, until the test ends.
func CaptureLogs(t testing.TB, level slog.Level) *bytes.Buffer {
	t.Helper()
	logs := new(bytes.Buffer)
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return logs
}
