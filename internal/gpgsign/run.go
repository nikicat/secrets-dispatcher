package gpgsign

import (
	"bytes"
	"context"
	"errors"
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

// Serves the gpg-sign subcommand, which git runs as
// `secrets-dispatcher gpg-sign --status-fd=2 -bsau <keyID>` with the object to sign on stdin.
// SIGINT and SIGTERM cancel the request on the daemon.
func Run(args []string, stdin io.Reader) ExitStatus {
	c := thinClient{debug: os.Getenv("SECRETS_DISPATCHER_DEBUG") == "1"}

	gpg := parseGPGArgs(args)
	// Git also calls gpg.program to verify signatures, list keys and print the
	// version. Only signing goes through the daemon, so every other call goes to
	// the real gpg unchanged. Git then reads gpg's own status lines and exit
	// status, and a good signature still verifies.
	if !gpg.sign {
		c.debugf("non-signing invocation, delegating to real gpg: %v", args)
		return passThroughToGPG(args, stdin)
	}

	c.debugf("keyID=%s args=%v", gpg.keyID, args)
	info, err := c.collectSignInfo(gpg.keyID, stdin)
	if err != nil {
		errorf("%v", err)
		return exitSystemError
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	outcome, err := c.sign(ctx, info)
	if err != nil {
		return reportFailure(ctx, err)
	}
	return outcome.report()
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
func (c thinClient) collectSignInfo(keyID string, stdin io.Reader) (*approval.GPGSignInfo, error) {
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

// Signs info through the remote socket that remoteSocketEnv names, or through
// the local daemon when it names none. Cancelling ctx cancels the request.
func (c thinClient) sign(ctx context.Context, info *approval.GPGSignInfo) (SignOutcome, error) {
	if socket := os.Getenv(remoteSocketEnv); socket != "" {
		c.debugf("remote socket=%s", socket)
		return SignRemote(ctx, socket, info)
	}
	return c.signLocal(ctx, info)
}

// Signs info through the local daemon's API socket and blocks until the
// request is resolved. Cancelling ctx cancels the request on the daemon.
func (c thinClient) signLocal(ctx context.Context, info *approval.GPGSignInfo) (SignOutcome, error) {
	client, err := c.locateLocalDaemon()
	if err != nil {
		return SignOutcome{}, err
	}

	// File the request with the daemon (SIGN-05).
	req, err := client.startSignRequest(ctx, info.RepoName, info)
	if err != nil {
		return SignOutcome{}, err
	}
	defer req.Close()
	c.debugf("request_id=%s", req.id)

	// Block until resolution (SIGN-08, ERR-01).
	outcome, err := req.Wait(ctx)
	if err != nil && ctx.Err() != nil {
		c.debugf("interrupted, cancelling request %s", req.id)
		// Best effort: ctx is done, so the cancel goes without it.
		if err := req.Cancel(context.WithoutCancel(ctx)); err != nil {
			c.debugf("cancel request failed: %v", err)
		}
	}
	return outcome, err
}

// Returns a client of the local daemon's API socket. Errors when the auth
// token or the socket cannot be found.
func (c thinClient) locateLocalDaemon() (*DaemonClient, error) {
	// Load auth token (ERR-01 if missing).
	stateDir, err := daemonStateDir()
	if err != nil {
		return nil, fmt.Errorf("cannot find the daemon's auth token: %w", err)
	}
	c.debugf("state dir=%s", stateDir)
	token, err := stateDir.ReadToken()
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("daemon not running (cannot read auth token): %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the daemon's auth token: %w", err)
	}

	runtimeDir, err := config.LookupRuntimeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot find the daemon's socket: %w", err)
	}
	socketPath := runtimeDir.APISocket()
	c.debugf("socket=%s", socketPath)
	return NewDaemonClient(socketPath, token), nil
}

// Returns the state directory that holds the daemon's API auth token. It is the
// state_dir that the default config file sets, or the default state directory
// when the file sets none. git runs gpg-sign without our flags, so a daemon
// started with another state directory or config file is not found.
func daemonStateDir() (config.StateDir, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return "", err
	}
	return cfg.StateDir.OrDefault()
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

// Returns the standard output of git run with the given arguments, untrimmed.
// Errors when git cannot start or exits non-zero.
func runGitCommand(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}
