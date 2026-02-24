package gpgsign

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Run is the entry point for the gpg-sign subcommand.
// It is called by git as: secrets-dispatcher gpg-sign --status-fd=2 -bsau <keyID>
// with the raw commit object on stdin.
// Returns the process exit code.
//
// Exit codes:
//   - 0: success (signature written to stdout, gpg status to stderr)
//   - 1: user denied the signing request or request timed out
//   - 2: system error (daemon unreachable, auth token missing, etc.)
//   - N: gpg's own exit code (ERR-02: propagate real gpg failures)
func Run(args []string, stdin io.Reader) int {
	debug := os.Getenv("SECRETS_DISPATCHER_DEBUG") == "1"

	// 1. Parse key ID from args.
	keyID := extractKeyID(args)
	if debug {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: debug: keyID=%s args=%v\n", keyID, args)
	}

	// 2. Read commit object from stdin (SIGN-02).
	commitBytes, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: failed to read stdin: %v\n", err)
		return 2
	}

	// 3. Parse commit object for display context (SIGN-02).
	author, committer, message, parentHash := ParseCommitObject(commitBytes)

	// 4. Collect git context (SIGN-03, SIGN-04).
	repoName := resolveRepoName(debug)
	changedFiles := collectChangedFiles(debug)

	// 5. Load auth token (ERR-01 if missing).
	token, err := loadAuthToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: daemon not running (cannot read auth token): %v\n", err)
		return 2
	}

	// 6. Determine socket path.
	socketPath := unixSocketPath()
	if debug {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: debug: socket=%s\n", socketPath)
	}

	// 7. Create daemon client.
	client := NewDaemonClient(socketPath, token)
	ctx := context.Background()

	// 8. WebSocket FIRST — ensures we don't miss the resolution event between
	// POST and the time we start listening (locked decision from CONTEXT.md).
	wsConn, err := client.DialWebSocket(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: daemon unreachable at %s. Is secrets-dispatcher running?\n", socketPath)
		return 2
	}
	defer wsConn.CloseNow()

	// 9. POST signing request to daemon (SIGN-05).
	reqID, err := client.PostSigningRequest(ctx, repoName, &approval.GPGSignInfo{
		RepoName:     repoName,
		CommitMsg:    message,
		Author:       author,
		Committer:    committer,
		KeyID:        keyID,
		ChangedFiles: changedFiles,
		ParentHash:   parentHash,
		CommitObject: string(commitBytes),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: failed to send signing request: %v\n", err)
		return 2
	}

	if debug {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: debug: request_id=%s\n", reqID)
	}

	// 10. Trap SIGINT/SIGTERM to cancel the signing request on interrupt.
	wsCtx, wsCancel := context.WithCancel(ctx)
	defer wsCancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			if debug {
				fmt.Fprintf(os.Stderr, "secrets-dispatcher: debug: interrupted, cancelling request %s\n", reqID)
			}
			wsCancel()
			// Best-effort cancel — use background context since wsCtx is cancelled.
			if err := client.CancelSigningRequest(context.Background(), reqID); err != nil && debug {
				fmt.Fprintf(os.Stderr, "secrets-dispatcher: debug: cancel request failed: %v\n", err)
			}
		case <-wsCtx.Done():
			// Normal exit path — nothing to do.
		}
		signal.Stop(sigCh)
	}()

	// 11. Block until resolution (SIGN-08, ERR-01, ERR-02).
	signature, gpgStatus, exitCode, denied, err := client.WaitForResolution(wsCtx, wsConn, reqID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: %v\n", err)
		return 1 // timeout is exit 1 per spec
	}

	if denied {
		fmt.Fprintln(os.Stderr, "secrets-dispatcher: signing request denied by user")
		return 1
	}

	if exitCode != 0 {
		// ERR-02: propagate gpg exit code.
		if len(gpgStatus) > 0 {
			os.Stderr.Write(gpgStatus) //nolint:errcheck
		}
		return exitCode
	}

	// SIGN-08: Write signature to stdout, status to stderr.
	os.Stdout.Write(signature) //nolint:errcheck
	if len(gpgStatus) > 0 {
		os.Stderr.Write(gpgStatus) //nolint:errcheck
	}
	return 0
}

// resolveRepoName runs git rev-parse --show-toplevel and returns the base name.
// Returns "unknown" on error.
func resolveRepoName(debug bool) string {
	out, err := runGitCommand("rev-parse", "--show-toplevel")
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "secrets-dispatcher: debug: resolveRepoName error: %v\n", err)
		}
		return "unknown"
	}
	return filepath.Base(strings.TrimSpace(out))
}

// collectChangedFiles runs git diff --cached --name-only and returns the file list.
// Returns nil on error.
func collectChangedFiles(debug bool) []string {
	out, err := runGitCommand("diff", "--cached", "--name-only")
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "secrets-dispatcher: debug: collectChangedFiles error: %v\n", err)
		}
		return nil
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// loadAuthToken reads the daemon's auth token from the cookie file at:
//
//	$XDG_STATE_HOME/secrets-dispatcher/.cookie
//
// Falls back to ~/.local/state/secrets-dispatcher/.cookie if XDG_STATE_HOME is unset.
func loadAuthToken() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	cookiePath := filepath.Join(stateHome, "secrets-dispatcher", ".cookie")
	data, err := os.ReadFile(cookiePath)
	if err != nil {
		return "", fmt.Errorf("read cookie file %s: %w", cookiePath, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("cookie file is empty: %s", cookiePath)
	}
	return token, nil
}

// unixSocketPath returns the path to the daemon's Unix socket:
//
//	$XDG_RUNTIME_DIR/secrets-dispatcher/api.sock
//
// Falls back to /run/user/<uid>/secrets-dispatcher/api.sock if XDG_RUNTIME_DIR is unset.
func unixSocketPath() string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(runtimeDir, "secrets-dispatcher", "api.sock")
}

// runGitCommand runs a git subcommand and returns trimmed stdout output.
func runGitCommand(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}
