package config

// The subcommand that runs the dispatcher.
const ServeCommand = "serve"

// The flag that names the config file a subcommand reads.
const ConfigFlag = "config"

// Returns the arguments, after the executable, that run serve on f.
func ServeArgs(f File) []string {
	return []string{ServeCommand, "--" + ConfigFlag, f.String()}
}
