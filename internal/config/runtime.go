package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// The per-user directory under $XDG_RUNTIME_DIR that holds the daemon's
// sockets and the other files that must not outlive the login session.
type RuntimeDir string

// The runtime directory as a systemd user unit writes it, where %t stands for
// $XDG_RUNTIME_DIR.
const SystemdRuntimeDir RuntimeDir = "%t/" + appName

// Reports that the current user has no runtime directory for the daemon's
// sockets.
var ErrNoRuntimeDir = errors.New("XDG_RUNTIME_DIR is not set to an absolute path")

// The file names in the runtime directory, sockets without their extension.
const (
	apiSocketName         = "api"
	sshAgentSocketName    = "ssh-agent"
	clientSocketsName     = "sockets"
	remoteSocketsName     = "remote"
	backendBusSocketName  = "backend-bus"
	keyringControlDirName = "keyring"
	trialConfigName       = "try-config.yaml"
)

// Returns the runtime directory of the current user. Errors with
// ErrNoRuntimeDir when XDG_RUNTIME_DIR is unset or not absolute. The XDG base
// directory spec gives this directory no fallback under the home directory.
func LookupRuntimeDir() (RuntimeDir, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(base) {
		return "", ErrNoRuntimeDir
	}
	return RuntimeDir(filepath.Join(base, appName)), nil
}

// Returns the socket where the daemon serves its API to clients of the same
// user, such as the gpg-sign thin client.
func (d RuntimeDir) APISocket() unixsock.Path { return d.dir().Socket(apiSocketName) }

// Returns the default socket of the SSH agent proxy.
func (d RuntimeDir) SSHAgentSocket() unixsock.Path { return d.dir().Socket(sshAgentSocketName) }

// Returns the default directory of per-client D-Bus sockets.
func (d RuntimeDir) ClientSockets() unixsock.Dir { return d.dir().Sub(clientSocketsName) }

// Returns the signing socket the daemon serves for the named remote (ADR 0011).
func (d RuntimeDir) RemoteSocket(name RemoteName) unixsock.Path {
	return d.dir().Sub(remoteSocketsName).Socket(string(name))
}

// Returns the socket of the private D-Bus that the backend keyring runs on.
func (d RuntimeDir) BackendBusSocket() unixsock.Path { return d.dir().Socket(backendBusSocketName) }

// Returns the control directory of the gnome-keyring backend.
func (d RuntimeDir) KeyringControlDir() unixsock.Dir { return d.dir().Sub(keyringControlDirName) }

// Returns the throwaway config of `secrets-dispatcher try`.
func (d RuntimeDir) TrialConfig() File { return File{filepath.Join(string(d), trialConfigName)} }

func (d RuntimeDir) dir() unixsock.Dir { return unixsock.Dir(d) }
