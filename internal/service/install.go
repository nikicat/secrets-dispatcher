// Package service manages the systemd user service for secrets-dispatcher.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const unitFileName = "secrets-dispatcher.service"

const unitTemplate = `[Unit]
Description=Secrets Dispatcher - D-Bus Secret Service approval proxy
Documentation=https://github.com/nikicat/secrets-dispatcher

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

// Options configures service installation.
type Options struct {
	// ConfigPath, if set, adds --config <path> to ExecStart.
	ConfigPath string
	// Start the service immediately after enabling.
	Start bool
}

// unitDir returns the systemd user unit directory.
// Uses $XDG_CONFIG_HOME/systemd/user/ with fallback to ~/.config/systemd/user/.
func unitDir() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "systemd", "user"), nil
}

// UnitPath returns the full path where the unit file is (or would be) installed.
func UnitPath() (string, error) {
	dir, err := unitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, unitFileName), nil
}

// Install writes the systemd user unit file, reloads systemd, and enables the service.
func Install(opts Options) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	execStart := self + " serve"
	if opts.ConfigPath != "" {
		execStart += " --config " + opts.ConfigPath
	}

	unitContent := fmt.Sprintf(unitTemplate, execStart)

	dir, err := unitDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(dir, unitFileName)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create unit dir: %w", err)
	}

	if err := os.WriteFile(unitPath, []byte(unitContent), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	fmt.Printf("Wrote unit file: %s\n", unitPath)

	if err := systemctlFunc("daemon-reload"); err != nil {
		return err
	}

	if err := systemctlFunc("enable", unitFileName); err != nil {
		return err
	}
	fmt.Printf("Enabled %s\n", unitFileName)

	if opts.Start {
		if err := systemctlFunc("start", unitFileName); err != nil {
			return err
		}
		fmt.Printf("Started %s\n", unitFileName)
	}

	return nil
}

// Uninstall stops and disables the service, removes the unit file, and reloads systemd.
func Uninstall() error {
	// Stop first (ignore error — may not be running).
	_ = systemctlFunc("stop", unitFileName)

	if err := systemctlFunc("disable", unitFileName); err != nil {
		return err
	}
	fmt.Printf("Disabled %s\n", unitFileName)

	dir, err := unitDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(dir, unitFileName)

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}
	fmt.Printf("Removed %s\n", unitPath)

	if err := systemctlFunc("daemon-reload"); err != nil {
		return err
	}

	return nil
}

// Status runs systemctl --user status for the service, printing output directly.
func Status() error {
	cmd := exec.Command("systemctl", "--user", "status", unitFileName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// systemctl status exits non-zero when inactive — not an error for us.
	cmd.Run()
	return nil
}

// systemctlFunc is the function used to run systemctl commands.
// Replaced in tests to avoid requiring a real systemd.
var systemctlFunc = systemctlExec

func systemctlExec(args ...string) error {
	fullArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", fullArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", args[0], err)
	}
	return nil
}
