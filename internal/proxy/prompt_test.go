package proxy

import (
	"log/slog"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

func TestPromptHandlerRejectsUnknownPrompt(t *testing.T) {
	prompts := newPromptRegistry()
	h := NewPromptHandler(nil, logging.New(slog.LevelError, "test"), prompts)

	err := h.Prompt(promptMessage("/org/freedesktop/secrets/prompt/1", ":1.10"), "")
	if err == nil {
		t.Fatal("expected unknown prompt to be rejected")
	}
	if err.Name != "org.freedesktop.Secret.Error.NoSuchObject" {
		t.Fatalf("expected no-such-object error, got %s", err.Name)
	}
}

func TestPromptHandlerRejectsDifferentSender(t *testing.T) {
	prompts := newPromptRegistry()
	path := dbus.ObjectPath("/org/freedesktop/secrets/prompt/1")
	prompts.register(path, ":1.10")
	h := NewPromptHandler(nil, logging.New(slog.LevelError, "test"), prompts)

	err := h.Dismiss(promptMessage(path, ":1.11"))
	if err == nil {
		t.Fatal("expected cross-sender prompt access to be rejected")
	}
	if err.Name != "org.freedesktop.DBus.Error.AccessDenied" {
		t.Fatalf("expected access denied, got %s", err.Name)
	}
}

func TestPromptRegistryIgnoresRootPrompt(t *testing.T) {
	prompts := newPromptRegistry()
	prompts.register("/", ":1.10")
	if _, ok := prompts.owner("/"); ok {
		t.Fatal("root prompt path should not be registered")
	}
}

func promptMessage(path dbus.ObjectPath, sender string) dbus.Message {
	return dbus.Message{Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldPath:   dbus.MakeVariant(path),
		dbus.FieldSender: dbus.MakeVariant(sender),
	}}
}
