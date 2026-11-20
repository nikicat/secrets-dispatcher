package companion

// Files that provision the companion-user experiment
// (docs/adr/0002-visibility-not-isolation.md).

// The companion daemon's system bus policy. It holds only allow rules, because
// the system bus already denies own="*" and method_call sends by default
// (/usr/share/dbus-1/system.conf).
const dbusPolicyTemplate = `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <!-- Companion user owns the bus name and can send to itself -->
  <policy user="{{.CompanionUser}}">
    <allow own="net.mowaka.SecretsDispatcher1"/>
    <allow send_destination="net.mowaka.SecretsDispatcher1"/>
  </policy>

  <!-- Desktop user can call methods on the companion daemon -->
  <policy user="{{.DesktopUser}}">
    <allow send_destination="net.mowaka.SecretsDispatcher1"/>
  </policy>
</busconfig>
`

// The companion daemon's systemd user unit. With Type=notify, systemd counts
// the daemon started only once it sends the sd_notify readiness message.
const systemdUnitTemplate = `[Unit]
Description=Secrets Dispatcher Companion Daemon
Documentation=https://github.com/nikicat/secrets-dispatcher
After=dbus.service
Requires=dbus.service

[Service]
Type=notify
ExecStart=/usr/local/bin/secrets-dispatcher daemon
Environment=HOME={{.CompanionHome}}
Environment=XDG_RUNTIME_DIR=/run/user/{{.CompanionUID}}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

// The PAM session hook that starts the companion daemon at login in the
// companion user's own systemd manager, without holding up the login.
// Provision installs the unit there unenabled. It runs only when a session
// opens (type=open_session), not again at logout.
const pamConfigTemplate = `# Managed by secrets-dispatcher provision. Do not edit manually.
# Start the companion daemon on desktop user login.
session optional pam_exec.so quiet type=open_session /usr/bin/systemctl --user --machine={{.CompanionUser}}@ start --no-block {{.DaemonUnit}}
`
