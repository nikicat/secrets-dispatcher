package proxy

import (
	"errors"
	"testing"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// mockDBusClient implements dbusClient for testing.
type mockDBusClient struct {
	pid      uint32
	pidErr   error
	uid      uint32
	uidErr   error
	unitName string
	unitErr  error
}

func (m *mockDBusClient) GetConnectionUnixProcessID(sender string) (uint32, error) {
	return m.pid, m.pidErr
}

func (m *mockDBusClient) GetConnectionUnixUser(sender string) (uint32, error) {
	return m.uid, m.uidErr
}

func (m *mockDBusClient) GetUnitByPID(pid uint32) (string, error) {
	return m.unitName, m.unitErr
}

func TestSenderInfoResolver_Resolve_AllSuccess(t *testing.T) {
	client := &mockDBusClient{
		pid:      12345,
		uid:      1000,
		unitName: "test.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	if info.Sender != ":1.123" {
		t.Errorf("expected sender :1.123, got %s", info.Sender)
	}
	if info.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", info.PID)
	}
	if info.UID != 1000 {
		t.Errorf("expected UID 1000, got %d", info.UID)
	}
	if info.UnitName != "test.service" {
		t.Errorf("expected unit_name test.service, got %s", info.UnitName)
	}
}

func TestSenderInfoResolver_Resolve_NoSystemd(t *testing.T) {
	// Simulates remote host without systemd or process not in a unit
	client := &mockDBusClient{
		pid:     12345,
		uid:     1000,
		unitErr: errors.New("PID 12345 does not belong to any loaded unit"),
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	if info.Sender != ":1.123" {
		t.Errorf("expected sender :1.123, got %s", info.Sender)
	}
	if info.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", info.PID)
	}
	if info.UID != 1000 {
		t.Errorf("expected UID 1000, got %d", info.UID)
	}
	if info.UnitName != "" {
		t.Errorf("expected empty unit_name, got %s", info.UnitName)
	}
}

func TestSenderInfoResolver_Resolve_PIDFails(t *testing.T) {
	client := &mockDBusClient{
		pidErr: errors.New("connection not found"),
		uid:    1000,
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	if info.Sender != ":1.123" {
		t.Errorf("expected sender :1.123, got %s", info.Sender)
	}
	if info.PID != 0 {
		t.Errorf("expected PID 0, got %d", info.PID)
	}
	if info.UID != 1000 {
		t.Errorf("expected UID 1000, got %d", info.UID)
	}
	// Unit lookup should be skipped when PID is 0
	if info.UnitName != "" {
		t.Errorf("expected empty unit_name, got %s", info.UnitName)
	}
}

func TestSenderInfoResolver_Resolve_UIDFails(t *testing.T) {
	client := &mockDBusClient{
		pid:      12345,
		uidErr:   errors.New("connection not found"),
		unitName: "test.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	if info.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", info.PID)
	}
	if info.UID != 0 {
		t.Errorf("expected UID 0, got %d", info.UID)
	}
	if info.UnitName != "test.service" {
		t.Errorf("expected unit_name test.service, got %s", info.UnitName)
	}
}

func TestSenderInfoResolver_Resolve_AllFail(t *testing.T) {
	client := &mockDBusClient{
		pidErr: errors.New("connection not found"),
		uidErr: errors.New("connection not found"),
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	// Should still return sender, with zeros for everything else
	if info.Sender != ":1.123" {
		t.Errorf("expected sender :1.123, got %s", info.Sender)
	}
	if info.PID != 0 {
		t.Errorf("expected PID 0, got %d", info.PID)
	}
	if info.UID != 0 {
		t.Errorf("expected UID 0, got %d", info.UID)
	}
	if info.UnitName != "" {
		t.Errorf("expected empty unit_name, got %s", info.UnitName)
	}
}

func TestSenderInfoResolver_Resolve_PartialInfo(t *testing.T) {
	// Verify that SenderInfo struct is properly initialized
	info := approval.SenderInfo{
		Sender:   ":1.456",
		PID:      12345,
		UID:      1000,
		UnitName: "test.service",
	}

	if info.Sender != ":1.456" {
		t.Errorf("expected sender :1.456, got %s", info.Sender)
	}
	if info.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", info.PID)
	}
	if info.UID != 1000 {
		t.Errorf("expected UID 1000, got %d", info.UID)
	}
	if info.UnitName != "test.service" {
		t.Errorf("expected unit_name test.service, got %s", info.UnitName)
	}
}

func TestNewSenderInfoResolver(t *testing.T) {
	// Test that NewSenderInfoResolver creates a valid resolver
	resolver := NewSenderInfoResolver(nil)
	if resolver == nil {
		t.Error("NewSenderInfoResolver returned nil")
	}
	if resolver.client == nil {
		t.Error("expected non-nil client")
	}
}
