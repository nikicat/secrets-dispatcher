// Package logging provides audit logging for secret access operations.
package logging

import (
	"context"
	"log/slog"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Writes one audit record per D-Bus call, through the default slog handler.
type Logger struct {
	*slog.Logger
	client approval.ClientName
}

// Returns a logger whose records name client.
func New(client approval.ClientName) *Logger {
	return &Logger{
		Logger: slog.Default(),
		client: client,
	}
}

// Logs a D-Bus method call with its result.
func (l *Logger) LogMethod(ctx context.Context, method string, args map[string]any, result string, err error) {
	attrs := []slog.Attr{
		slog.String("client", string(l.client)),
		slog.String("method", method),
		slog.String("result", result),
	}
	for k, v := range args {
		attrs = append(attrs, slog.Any(k, v))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}

	l.LogAttrs(ctx, slog.LevelInfo, "dbus_call", attrs...)
}

// Logs a GetSecrets call with the requested items.
func (l *Logger) LogGetSecrets(ctx context.Context, items []dbus.ObjectPath, result string, err error) {
	l.LogMethod(ctx, "GetSecrets", map[string]any{"items": items}, result, err)
}

func (l *Logger) LogOpenSession(ctx context.Context, algorithm string, sessionPath string, result string, err error) {
	l.LogMethod(ctx, "OpenSession", map[string]any{
		"algorithm": algorithm,
		"session":   sessionPath,
	}, result, err)
}

func (l *Logger) LogSearchItems(ctx context.Context, attributes map[string]string, unlocked, locked int, result string, err error) {
	l.LogMethod(ctx, "SearchItems", map[string]any{
		"attributes":     attributes,
		"unlocked_count": unlocked,
		"locked_count":   locked,
	}, result, err)
}

func (l *Logger) LogUnlock(ctx context.Context, objects []dbus.ObjectPath, unlocked int, result string, err error) {
	l.LogMethod(ctx, "Unlock", map[string]any{
		"objects":        objects,
		"unlocked_count": unlocked,
	}, result, err)
}

func (l *Logger) LogReadAlias(ctx context.Context, alias string, collection string, result string, err error) {
	l.LogMethod(ctx, "ReadAlias", map[string]any{
		"alias":      alias,
		"collection": collection,
	}, result, err)
}

func (l *Logger) LogItemGetSecret(ctx context.Context, itemPath string, result string, err error) {
	l.LogMethod(ctx, "Item.GetSecret", map[string]any{"item": itemPath}, result, err)
}
