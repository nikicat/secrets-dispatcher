package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Returns a logger for client "c" whose records decode into the returned
// function's result.
func recordingLogger(t *testing.T) (*Logger, func() map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	l := &Logger{Logger: slog.New(slog.NewJSONHandler(&buf, nil)), client: "c"}
	return l, func() map[string]any {
		var rec map[string]any
		require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
		delete(rec, "time")
		buf.Reset()
		return rec
	}
}

func TestLogCreateItem(t *testing.T) {
	l, record := recordingLogger(t)
	ctx := context.Background()

	l.LogItemCreated(ctx, "/col", "/col/i1")
	assert.Equal(t, map[string]any{
		"level": "INFO", "msg": "dbus_call", "client": "c", "method": "Collection.CreateItem",
		"result": "ok", "collection": "/col", "item": "/col/i1",
	}, record())

	l.LogCreateItem(ctx, "/col", ResultIgnored, nil)
	assert.Equal(t, map[string]any{
		"level": "INFO", "msg": "dbus_call", "client": "c", "method": "Collection.CreateItem",
		"result": "ignored", "collection": "/col",
	}, record())

	l.LogCreateItem(ctx, "/col", ResultDenied, errors.New("no"))
	assert.Equal(t, map[string]any{
		"level": "INFO", "msg": "dbus_call", "client": "c", "method": "Collection.CreateItem",
		"result": "denied", "collection": "/col", "error": "no",
	}, record())
}
