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
	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// Exit codes of the thin client, besides gpg's own (ERR-02).
const (
	exitOK          = 0
	exitRefused     = 1 // denied by the user or timed out
	exitSystemError = 2 // daemon unreachable, auth token missing, etc.
)

// Serves the gpg-sign subcommand, which git runs as
// `secrets-dispatcher gpg-sign --status-fd=2 -bsau <keyID>` with the object to sign on stdin.
// Returns the process exit code: one of the exit constants, or gpg's own (ERR-02).
func Run(args []string, stdin io.Reader) int {
	c := thinClient{debug: os.Getenv("SECRETS_DISPATCHER_DEBUG") == "1"}

	// Delegate non-signing invocations to the real gpg. git also calls
	// gpg.program to *verify* signatures (verify-commit, verify-tag, log
	// --show-signature, merge/pull verification) and to list keys or probe
	// --version; this proxy only knows how to create signatures via the daemon.
	// Anything that is not a sign request goes straight to real gpg, so those
	// operations work exactly as if gpg.program were plain gpg (see
	// passThroughToGPG).
	if !isSignRequest(args) {
		c.debugf("non-signing invocation, delegating to real gpg: %v", args)
		return passThroughToGPG(args, stdin)
	}

	info, err := c.collectSignInfo(args, stdin)
	if err != nil {
		errorf("%v", err)
		return exitSystemError
	}
	if socket := os.Getenv(remoteSocketEnv); socket != "" {
		return c.runRemote(socket, info)
	}
	return c.runLocal(info)
}

// The process-wide settings of one gpg-sign invocation.
type thinClient struct {
	debug bool // set by SECRETS_DISPATCHER_DEBUG=1
}

// The prefix of every message the thin client itself writes to stderr. gpg's
// status lines go out unchanged, because git reads them.
const stderrPrefix = "secrets-dispatcher: "

// Writes a user-facing error line to stderr.
func errorf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, stderrPrefix+format+"\n", a...)
}

// Writes a debug line to stderr when debugging is on.
func (c thinClient) debugf(format string, a ...any) {
	if c.debug {
		fmt.Fprintf(os.Stderr, stderrPrefix+"debug: "+format+"\n", a...)
	}
}

// Reads the object to sign from stdin and returns it with its git context for
// the approval prompt.
func (c thinClient) collectSignInfo(args []string, stdin io.Reader) (*approval.GPGSignInfo, error) {
	keyID := extractKeyID(args)
	c.debugf("keyID=%s args=%v", keyID, args)

	// Read commit object from stdin (SIGN-02).
	commitBytes, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("failed to read stdin: %w", err)
	}

	// Parse the signed payload for display context (SIGN-02). git signs
	// commits, annotated tags, and push certificates through this same path;
	// detect which so the approval prompt shows the right fields.
	payload := ParseSignedPayload(commitBytes)

	// Collect git context (SIGN-03, SIGN-04). Changed files describe a
	// commit's staged tree — they are meaningless for tag/push signing and
	// could mislead both the prompt and trusted-signer matching, so gather them
	// for commits only.
	repoName := c.resolveRepoName()
	var changedFiles []string
	if payload.Kind == KindCommit {
		changedFiles = c.collectChangedFiles()
	}
	return &approval.GPGSignInfo{
		RepoName:     repoName,
		Kind:         string(payload.Kind),
		CommitMsg:    payload.Message,
		Author:       payload.Signer,
		Committer:    payload.Committer,
		KeyID:        keyID,
		ChangedFiles: changedFiles,
		ParentHash:   payload.ParentHash,
		TagName:      payload.TagName,
		Target:       payload.Target,
		Pushee:       payload.Pushee,
		CommitObject: string(commitBytes),
	}, nil
}

// Signs info through the local daemon's API socket and returns the process exit code.
func (c thinClient) runLocal(info *approval.GPGSignInfo) int {
	// Load auth token (ERR-01 if missing).
	token, err := loadAuthToken()
	if err != nil {
		errorf("daemon not running (cannot read auth token): %v", err)
		return exitSystemError
	}

	runtimeDir, err := config.LookupRuntimeDir()
	if err != nil {
		errorf("cannot find the daemon's socket: %v", err)
		return exitSystemError
	}
	socketPath := runtimeDir.APISocket()
	c.debugf("socket=%s", socketPath)

	client := NewDaemonClient(socketPath, token)
	ctx := context.Background()

	// WebSocket FIRST — ensures we don't miss the resolution event between
	// POST and the time we start listening (locked decision from CONTEXT.md).
	wsConn, err := client.DialWebSocket(ctx)
	if err != nil {
		errorf("daemon unreachable at %s. Is secrets-dispatcher running?", socketPath)
		return exitSystemError
	}
	defer wsConn.CloseNow()

	// POST signing request to daemon (SIGN-05).
	reqID, err := client.PostSigningRequest(ctx, info.RepoName, info)
	if err != nil {
		errorf("failed to send signing request: %v", err)
		return exitSystemError
	}
	c.debugf("request_id=%s", reqID)

	// Trap SIGINT/SIGTERM to cancel the signing request on interrupt.
	wsCtx, wsCancel := context.WithCancel(ctx)
	defer wsCancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			c.debugf("interrupted, cancelling request %s", reqID)
			wsCancel()
			// Best-effort cancel — use background context since wsCtx is cancelled.
			if err := client.CancelSigningRequest(context.Background(), reqID); err != nil {
				c.debugf("cancel request failed: %v", err)
			}
		case <-wsCtx.Done():
			// Normal exit path — nothing to do.
		}
		signal.Stop(sigCh)
	}()

	// Block until resolution (SIGN-08, ERR-01, ERR-02).
	signature, gpgStatus, exitCode, denied, err := client.WaitForResolution(wsCtx, wsConn, reqID)
	if err != nil {
		errorf("%v", err)
		return exitRefused
	}

	if denied {
		errorf("signing request denied by user")
		return exitRefused
	}
	return GPGOutput{Signature: signature, Status: gpgStatus, ExitCode: exitCode}.Emit()
}

// Signs info through the remote socket at socketPath and returns the process exit code.
func (c thinClient) runRemote(socketPath string, info *approval.GPGSignInfo) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	c.debugf("remote socket=%s", socketPath)
	res, err := SignRemote(ctx, socketPath, info)
	if err != nil {
		errorf("%v", err)
		return exitSystemError
	}
	switch res.Result {
	case approval.ResolutionApproved:
		if res.GPGOutput == nil {
			errorf("approved, but the daemon sent no signature")
			return exitSystemError
		}
		return res.GPGOutput.Emit()
	case approval.ResolutionExpired:
		errorf("signing request timed out")
		return exitRefused
	case approval.ResolutionDenied, approval.ResolutionCancelled:
		errorf("signing request denied by user")
		return exitRefused
	}
	errorf("unexpected result %q from the daemon", res.Result)
	return exitSystemError
}

// gpg's result for one sign request, as the thin client hands it back to git.
type GPGOutput struct {
	Signature []byte `json:"signature,omitempty"`
	Status    []byte `json:"gpg_status,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
}

// Writes gpg's status lines to stderr and, when gpg succeeded, the signature
// to stdout (SIGN-08). Returns the process exit code, which is gpg's own (ERR-02).
func (o GPGOutput) Emit() int {
	if len(o.Status) > 0 {
		os.Stderr.Write(o.Status) //nolint:errcheck
	}
	if o.ExitCode != 0 {
		return o.ExitCode
	}
	os.Stdout.Write(o.Signature) //nolint:errcheck
	return exitOK
}

// resolveRepoName runs git rev-parse --show-toplevel and returns the base name.
// Returns "unknown" on error.
func (c thinClient) resolveRepoName() string {
	out, err := runGitCommand("rev-parse", "--show-toplevel")
	if err != nil {
		c.debugf("resolveRepoName error: %v", err)
		return "unknown"
	}
	return filepath.Base(strings.TrimSpace(out))
}

// collectChangedFiles runs git diff --cached --name-only and returns the file list.
// Returns nil on error.
func (c thinClient) collectChangedFiles() []string {
	out, err := runGitCommand("diff", "--cached", "--name-only")
	if err != nil {
		c.debugf("collectChangedFiles error: %v", err)
		return nil
	}
	var files []string
	for f := range strings.SplitSeq(out, "\n") {
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
