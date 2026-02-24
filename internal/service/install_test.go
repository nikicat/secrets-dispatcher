package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesUnitFile(t *testing.T) {
	// Override XDG_CONFIG_HOME to write into a temp dir.
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// We can't run systemctl in tests, so override the function.
	origSystemctl := systemctlFunc
	var calls []string
	systemctlFunc = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { systemctlFunc = origSystemctl })

	err := Install(Options{})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	unitPath := filepath.Join(tmpDir, "systemd", "user", unitFileName)
	content, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit file: %v", err)
	}

	s := string(content)
	if !strings.Contains(s, "ExecStart=") {
		t.Error("unit file missing ExecStart")
	}
	if !strings.Contains(s, "serve") {
		t.Error("ExecStart should contain 'serve'")
	}
	if strings.Contains(s, "--config") {
		t.Error("ExecStart should not contain --config when ConfigPath is empty")
	}
	if !strings.Contains(s, "WantedBy=default.target") {
		t.Error("unit file missing WantedBy=default.target")
	}

	// Verify systemctl calls.
	expected := []string{"daemon-reload", "enable " + unitFileName}
	if len(calls) != len(expected) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(expected), len(calls), calls)
	}
	for i, want := range expected {
		if calls[i] != want {
			t.Errorf("systemctl call %d: got %q, want %q", i, calls[i], want)
		}
	}
}

func TestInstallWithConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	origSystemctl := systemctlFunc
	systemctlFunc = func(args ...string) error { return nil }
	t.Cleanup(func() { systemctlFunc = origSystemctl })

	err := Install(Options{ConfigPath: "/etc/secrets-dispatcher/config.yaml"})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	unitPath := filepath.Join(tmpDir, "systemd", "user", unitFileName)
	content, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit file: %v", err)
	}

	if !strings.Contains(string(content), "--config /etc/secrets-dispatcher/config.yaml") {
		t.Error("ExecStart should contain --config flag")
	}
}

func TestInstallWithStart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	origSystemctl := systemctlFunc
	var calls []string
	systemctlFunc = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { systemctlFunc = origSystemctl })

	err := Install(Options{Start: true})
	if err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	expected := []string{"daemon-reload", "enable " + unitFileName, "start " + unitFileName}
	if len(calls) != len(expected) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(expected), len(calls), calls)
	}
	for i, want := range expected {
		if calls[i] != want {
			t.Errorf("systemctl call %d: got %q, want %q", i, calls[i], want)
		}
	}
}

func TestUninstall(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Create a fake unit file to remove.
	unitDir := filepath.Join(tmpDir, "systemd", "user")
	os.MkdirAll(unitDir, 0755)
	unitPath := filepath.Join(unitDir, unitFileName)
	os.WriteFile(unitPath, []byte("fake"), 0644)

	origSystemctl := systemctlFunc
	var calls []string
	systemctlFunc = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { systemctlFunc = origSystemctl })

	err := Uninstall()
	if err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}

	// Unit file should be gone.
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Error("unit file should have been removed")
	}

	// Verify systemctl calls: stop, disable, daemon-reload.
	expected := []string{"stop " + unitFileName, "disable " + unitFileName, "daemon-reload"}
	if len(calls) != len(expected) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(expected), len(calls), calls)
	}
	for i, want := range expected {
		if calls[i] != want {
			t.Errorf("systemctl call %d: got %q, want %q", i, calls[i], want)
		}
	}
}

func TestUnitPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	p, err := UnitPath()
	if err != nil {
		t.Fatalf("UnitPath() error: %v", err)
	}

	want := filepath.Join(tmpDir, "systemd", "user", unitFileName)
	if p != want {
		t.Errorf("UnitPath() = %q, want %q", p, want)
	}
}
