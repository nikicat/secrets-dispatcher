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

// Returns a logger whose records name the given client.
func New(client approval.ClientName) *Logger {
	return &Logger{
		Logger: slog.Default(),
		client: client,
	}
}

// Outcome of one D-Bus call, as its audit record names it.
type Result string

const (
	ResultOK     Result = "ok"
	ResultDenied Result = "denied"
	ResultError  Result = "error"
	// The call was a write that an ignore rule dropped, and the client was told
	// it succeeded.
	ResultIgnored Result = "ignored"
)

// Logs a D-Bus method call with its result.
func (l *Logger) logMethod(ctx context.Context, method string, result Result, err error, args ...slog.Attr) {
	attrs := []slog.Attr{
		slog.String("client", string(l.client)),
		slog.String("method", method),
		slog.String("result", string(result)),
	}
	attrs = append(attrs, args...)
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}

	l.LogAttrs(ctx, slog.LevelInfo, "dbus_call", attrs...)
}

// Logs a GetSecrets call with the requested items.
func (l *Logger) LogGetSecrets(ctx context.Context, items []dbus.ObjectPath, result Result, err error) {
	l.logMethod(ctx, "GetSecrets", result, err, slog.Any("items", items))
}

func (l *Logger) LogOpenSession(ctx context.Context, algorithm string, sessionPath dbus.ObjectPath, result Result, err error) {
	l.logMethod(ctx, "OpenSession", result, err,
		slog.String("algorithm", algorithm),
		slog.String("session", string(sessionPath)))
}

func (l *Logger) LogSearchItems(ctx context.Context, attributes map[string]string, unlocked, locked int, result Result, err error) {
	l.logMethod(ctx, "SearchItems", result, err,
		slog.Any("attributes", attributes),
		slog.Int("unlocked_count", unlocked),
		slog.Int("locked_count", locked))
}

func (l *Logger) LogUnlock(ctx context.Context, objects []dbus.ObjectPath, unlocked int, result Result, err error) {
	l.logMethod(ctx, "Unlock", result, err,
		slog.Any("objects", objects),
		slog.Int("unlocked_count", unlocked))
}

func (l *Logger) LogReadAlias(ctx context.Context, alias string, collection dbus.ObjectPath, result Result, err error) {
	l.logMethod(ctx, "ReadAlias", result, err,
		slog.String("alias", alias),
		slog.String("collection", string(collection)))
}

func (l *Logger) LogCollectionDelete(ctx context.Context, collection dbus.ObjectPath, result Result, err error) {
	l.logMethod(ctx, "Collection.Delete", result, err, slog.String("collection", string(collection)))
}

func (l *Logger) LogCollectionSearchItems(ctx context.Context, collection dbus.ObjectPath, attributes map[string]string, count int, result Result, err error) {
	l.logMethod(ctx, "Collection.SearchItems", result, err,
		slog.String("collection", string(collection)),
		slog.Any("attributes", attributes),
		slog.Int("count", count))
}

// Logs a CreateItem call on the given collection that created no item.
// LogItemCreated logs the one that did.
func (l *Logger) LogCreateItem(ctx context.Context, collection dbus.ObjectPath, result Result, err error) {
	l.logMethod(ctx, "Collection.CreateItem", result, err, slog.String("collection", string(collection)))
}

// Logs a CreateItem call on the given collection that created the given item.
func (l *Logger) LogItemCreated(ctx context.Context, collection, item dbus.ObjectPath) {
	l.logMethod(ctx, "Collection.CreateItem", ResultOK, nil,
		slog.String("collection", string(collection)),
		slog.String("item", string(item)))
}

func (l *Logger) LogItemDelete(ctx context.Context, item dbus.ObjectPath, result Result, err error) {
	l.logMethod(ctx, "Item.Delete", result, err, slog.String("item", string(item)))
}

func (l *Logger) LogItemGetSecret(ctx context.Context, item dbus.ObjectPath, result Result, err error) {
	l.logMethod(ctx, "Item.GetSecret", result, err, slog.String("item", string(item)))
}

func (l *Logger) LogItemSetSecret(ctx context.Context, item dbus.ObjectPath, result Result, err error) {
	l.logMethod(ctx, "Item.SetSecret", result, err, slog.String("item", string(item)))
}
