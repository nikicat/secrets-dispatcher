package service

// A service setup command, as the CLI names it.
type cliCommand string

// The service setup's commands. The hints that tell the user to run one
// spell it from these.
const (
	InstallCommand   cliCommand = "service install"
	UninstallCommand cliCommand = "service uninstall"
	StatusCommand    cliCommand = "service status"
)

// Returns the command line that runs c, quoted as a hint quotes it.
func (c cliCommand) hint() string { return "`secrets-dispatcher " + string(c) + "`" }
