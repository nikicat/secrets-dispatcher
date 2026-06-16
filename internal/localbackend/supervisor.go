package localbackend

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

const defaultReadyTimeout = 10 * time.Second

// Options configure a supervised local backend bus and Secret Service backend.
type Options struct {
	BackendCommand string
	RuntimeDir     string
	ReadyTimeout   time.Duration
	Log            *slog.Logger
}

// Supervisor owns a private D-Bus daemon and a backend Secret Service process.
type Supervisor struct {
	address    string
	runtimeDir string
	log        *slog.Logger

	dbusCmd    *exec.Cmd
	backendCmd *exec.Cmd

	dbusDone    chan error
	backendDone chan error
	stopOnce    sync.Once
}

// Start launches a private D-Bus daemon and a backend Secret Service process.
// The backend address is intentionally runtime-only: it is not written to config,
// unit files, or logs. This reduces accidental bypass by normal desktop apps; it
// is not a hard sandbox boundary against malicious same-UID code.
func Start(ctx context.Context, opts Options) (*Supervisor, error) {
	if opts.BackendCommand == "" {
		return nil, errors.New("backend command is required")
	}
	if opts.RuntimeDir == "" {
		return nil, errors.New("runtime directory is required")
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	readyTimeout := opts.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = defaultReadyTimeout
	}

	backendArgs, err := SplitCommand(opts.BackendCommand)
	if err != nil {
		return nil, fmt.Errorf("parse backend command: %w", err)
	}
	if len(backendArgs) == 0 {
		return nil, errors.New("backend command is empty")
	}

	baseDir := filepath.Join(opts.RuntimeDir, "secrets-dispatcher", "private")
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, fmt.Errorf("create private backend dir: %w", err)
	}
	tmpDir, err := os.MkdirTemp(baseDir, "backend-")
	if err != nil {
		return nil, fmt.Errorf("create private backend runtime dir: %w", err)
	}
	if err := os.Chmod(tmpDir, 0700); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("chmod private backend runtime dir: %w", err)
	}

	dbusPath, err := exec.LookPath("dbus-daemon")
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("find dbus-daemon: %w", err)
	}

	dbusCmd := exec.CommandContext(ctx, dbusPath,
		"--session",
		"--nofork",
		"--nopidfile",
		"--print-address=1",
		"--address=unix:tmpdir="+tmpDir,
	)
	dbusCmd.SysProcAttr = childProcAttrs()
	dbusCmd.Stderr = os.Stderr
	stdout, err := dbusCmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("open dbus-daemon stdout: %w", err)
	}
	if err := dbusCmd.Start(); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("start dbus-daemon: %w", err)
	}

	address, err := readBusAddress(ctx, stdout, readyTimeout)
	if err != nil {
		_ = killProcess(dbusCmd)
		_ = dbusCmd.Wait()
		os.RemoveAll(tmpDir)
		return nil, err
	}

	backendArgs = expandBackendPlaceholders(backendArgs, tmpDir, opts.RuntimeDir)
	backendCmd := exec.CommandContext(ctx, backendArgs[0], backendArgs[1:]...)
	backendCmd.SysProcAttr = childProcAttrs()
	backendCmd.Env = withEnv(os.Environ(), "DBUS_SESSION_BUS_ADDRESS", address)
	backendCmd.Stdout = os.Stderr
	backendCmd.Stderr = os.Stderr
	if err := backendCmd.Start(); err != nil {
		_ = killProcess(dbusCmd)
		_ = dbusCmd.Wait()
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("start backend: %w", err)
	}

	s := &Supervisor{
		address:     address,
		runtimeDir:  tmpDir,
		log:         log,
		dbusCmd:     dbusCmd,
		backendCmd:  backendCmd,
		dbusDone:    make(chan error, 1),
		backendDone: make(chan error, 1),
	}
	go func() { s.dbusDone <- dbusCmd.Wait() }()
	go func() { s.backendDone <- backendCmd.Wait() }()

	if err := waitForBackendName(ctx, address, readyTimeout); err != nil {
		s.Stop()
		return nil, err
	}

	log.Info("private local Secret Service backend started")
	return s, nil
}

// Address returns the private D-Bus address for the proxy's backend connection.
func (s *Supervisor) Address() string {
	return s.address
}

// Wait blocks until the supervisor context is cancelled or a child exits.
func (s *Supervisor) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-s.backendDone:
		if err == nil {
			return errors.New("backend exited")
		}
		return fmt.Errorf("backend exited: %w", err)
	case err := <-s.dbusDone:
		if err == nil {
			return errors.New("private dbus-daemon exited")
		}
		return fmt.Errorf("private dbus-daemon exited: %w", err)
	}
}

// Stop terminates child processes and removes the private runtime directory.
func (s *Supervisor) Stop() {
	s.stopOnce.Do(func() {
		_ = killProcess(s.backendCmd)
		_ = killProcess(s.dbusCmd)
		if s.runtimeDir != "" {
			_ = os.RemoveAll(s.runtimeDir)
		}
	})
}

func readBusAddress(ctx context.Context, r io.Reader, timeout time.Duration) (string, error) {
	type result struct {
		address string
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		ch <- result{address: strings.TrimSpace(line), err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", errors.New("timed out waiting for private dbus-daemon address")
	case res := <-ch:
		if res.err != nil && res.address == "" {
			return "", fmt.Errorf("read private dbus-daemon address: %w", res.err)
		}
		if res.address == "" {
			return "", errors.New("private dbus-daemon printed empty address")
		}
		return res.address, nil
	}
}

func waitForBackendName(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := dbus.Connect(address)
		if err == nil {
			var hasOwner bool
			call := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, dbustypes.BusName)
			if call.Err == nil {
				_ = call.Store(&hasOwner)
			}
			conn.Close()
			if hasOwner {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("backend did not claim %s within %s", dbustypes.BusName, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func expandBackendPlaceholders(args []string, backendDir, runtimeDir string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		arg = strings.ReplaceAll(arg, "%B", backendDir)
		arg = strings.ReplaceAll(arg, "%R", runtimeDir)
		out[i] = arg
	}
	return out
}

func withEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func childProcAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// SplitCommand splits a command line using simple shell-like quotes. It does not
// invoke a shell or expand variables; callers pass the returned argv directly to exec.
func SplitCommand(command string) ([]string, error) {
	var args []string
	var b strings.Builder
	var quote rune
	escaped := false
	inToken := false

	flush := func() {
		args = append(args, b.String())
		b.Reset()
		inToken = false
	}

	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			inToken = true
			continue
		}
		if r == '\\' {
			escaped = true
			inToken = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				inToken = true
				continue
			}
			b.WriteRune(r)
			inToken = true
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			inToken = true
		case ' ', '\t', '\n', '\r':
			if inToken {
				flush()
			}
		default:
			b.WriteRune(r)
			inToken = true
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if inToken {
		flush()
	}
	return args, nil
}
