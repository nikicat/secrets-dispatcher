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
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// Serves the gpg-sign subcommand that git runs as gpg.program (ADR 0008). SIGINT
// and SIGTERM cancel a signing request on the daemon.
func Run(args []string, stdin io.Reader) ExitStatus {
	c := thinClient{debug: os.Getenv("SECRETS_DISPATCHER_DEBUG") == "1"}

	c.debugf("args=%v", args)
	call, err := parseGPGArgs(args)
	if err != nil {
		return systemError(err)
	}
	return call.run(c, stdin)
}

func (s signCall) run(c thinClient, stdin io.Reader) ExitStatus {
	info, err := collectSignInfo(s.keyID, stdin)
	if err != nil {
		return systemError(err)
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

// Writes the given error to stderr and returns exitSystemError.
func systemError(err error) ExitStatus {
	errorf("%v", err)
	return exitSystemError
}

// Writes a debug line to stderr when debugging is on.
func (c thinClient) debugf(format string, a ...any) {
	if c.debug {
		fmt.Fprintf(os.Stderr, stderrPrefix+"debug: "+format+"\n", a...)
	}
}

// Reads the object to sign from stdin and returns it with its git context. Errors
// when git cannot name the repository, and as approval.NewGPGSignInfo does (ADR 0008).
func collectSignInfo(keyID approval.GPGKeyID, stdin io.Reader) (*approval.GPGSignInfo, error) {
	object, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("failed to read stdin: %w", err)
	}
	repo, err := gitRepoName()
	if err != nil {
		return nil, fmt.Errorf("find the repository: %w", err)
	}
	return approval.NewGPGSignInfo(object, repo, keyID, gitStagedFiles{})
}

// Signs info through the remote socket that remoteSocketEnv names, or through
// the local daemon when it names none. Cancelling ctx cancels the request.
func (c thinClient) sign(ctx context.Context, info *approval.GPGSignInfo) (SignOutcome, error) {
	if socket := unixsock.Path(os.Getenv(remoteSocketEnv)); socket != "" {
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

	req, err := client.startSignRequest(ctx, info)
	if err != nil {
		return SignOutcome{}, err
	}
	defer req.Close()
	c.debugf("request_id=%s", req.id)

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

// Returns the state directory that holds the daemon's API auth token, as the
// default config file sets it (ADR 0008).
func daemonStateDir() (config.StateDir, error) {
	source, err := config.DefaultSource()
	if err != nil {
		return "", err
	}
	cfg, err := source.Load()
	if err != nil {
		return "", err
	}
	return cfg.StateDir.OrDefault()
}

// Returns the name of the work tree's top-level directory. Errors as
// runGitCommand does, for example outside a work tree.
func gitRepoName() (approval.RepoName, error) {
	out, err := runGitCommand("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return approval.RepoName(filepath.Base(strings.TrimSpace(out))), nil
}

// The files staged in the work tree, as git lists them.
type gitStagedFiles struct{}

// Errors as runGitCommand does.
func (gitStagedFiles) List() ([]string, error) {
	out, err := runGitCommand("diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	var files []string
	for f := range strings.SplitSeq(out, "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
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
