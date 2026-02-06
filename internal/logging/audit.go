// Package logging provides audit logging for secret access operations.
package logging

import (
	"context"
	"log/slog"
	"os"
)

// Logger wraps slog for structured audit logging.
type Logger struct {
	*slog.Logger
	client string
}

// New creates a new audit logger that writes JSON to stderr.
func New(level slog.Level, client string) *Logger {
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
	return &Logger{
		Logger: slog.New(handler),
		client: client,
	}
}

// WithClient returns a new Logger with the specified client name.
func (l *Logger) WithClient(client string) *Logger {
	return &Logger{
		Logger: l.Logger,
		client: client,
	}
}

// LogMethod logs a D-Bus method call with its result.
func (l *Logger) LogMethod(ctx context.Context, method string, args map[string]any, result string, err error) {
	attrs := []slog.Attr{
		slog.String("client", l.client),
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

// LogGetSecrets logs a GetSecrets call with the requested items.
func (l *Logger) LogGetSecrets(ctx context.Context, items []string, result string, err error) {
	l.LogMethod(ctx, "GetSecrets", map[string]any{"items": items}, result, err)
}

// LogOpenSession logs an OpenSession call.
func (l *Logger) LogOpenSession(ctx context.Context, algorithm string, sessionPath string, result string, err error) {
	l.LogMethod(ctx, "OpenSession", map[string]any{
		"algorithm": algorithm,
		"session":   sessionPath,
	}, result, err)
}

// LogSearchItems logs a SearchItems call.
func (l *Logger) LogSearchItems(ctx context.Context, attributes map[string]string, unlocked, locked int, result string, err error) {
	l.LogMethod(ctx, "SearchItems", map[string]any{
		"attributes":     attributes,
		"unlocked_count": unlocked,
		"locked_count":   locked,
	}, result, err)
}

// LogUnlock logs an Unlock call.
func (l *Logger) LogUnlock(ctx context.Context, objects []string, unlocked int, result string, err error) {
	l.LogMethod(ctx, "Unlock", map[string]any{
		"objects":        objects,
		"unlocked_count": unlocked,
	}, result, err)
}

// LogReadAlias logs a ReadAlias call.
func (l *Logger) LogReadAlias(ctx context.Context, alias string, collection string, result string, err error) {
	l.LogMethod(ctx, "ReadAlias", map[string]any{
		"alias":      alias,
		"collection": collection,
	}, result, err)
}

// LogItemGetSecret logs an Item.GetSecret call.
func (l *Logger) LogItemGetSecret(ctx context.Context, itemPath string, result string, err error) {
	l.LogMethod(ctx, "Item.GetSecret", map[string]any{"item": itemPath}, result, err)
}
