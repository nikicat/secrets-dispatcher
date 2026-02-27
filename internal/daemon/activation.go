package daemon

import "fmt"

// ActivationFilePath is the install location for the D-Bus service activation file.
const ActivationFilePath = "/usr/share/dbus-1/system-services/net.mowaka.SecretsDispatcher1.service"

// ActivationFileContent returns the D-Bus service activation file content for
// the given companion username (e.g. "secrets-nb").
//
// Install to ActivationFilePath so that dbus-daemon automatically starts the
// daemon when a caller invokes any method on net.mowaka.SecretsDispatcher1.
func ActivationFileContent(companionUser string) string {
	return fmt.Sprintf(`[D-BUS Service]
Name=net.mowaka.SecretsDispatcher1
Exec=/usr/local/bin/secrets-dispatcher daemon
User=%s
`, companionUser)
}
