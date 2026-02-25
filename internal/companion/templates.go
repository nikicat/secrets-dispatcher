package companion

// Template strings for provisioning artifacts. text/template is used for
// rendering so template variables are clear and composable.

// dbusPolicyTemplate is the D-Bus system bus policy file for the companion daemon.
// It is modeled on Pattern 5 from RESEARCH.md (verified against /usr/share/dbus-1/system.conf).
// The system bus already denies own="*" and method_call sends by default, so we
// only need to add allow (punch-hole) rules.
//
// Install location: /usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf
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

// systemdUnitTemplate is the systemd user unit file for the companion daemon.
// Installed to companion user's ~/.config/systemd/user/.
// Type=notify requires sd-notify readiness notification before systemd considers it started.
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

// pamConfigTemplate is the PAM session hook that starts the companion daemon on login.
// Installed to /etc/pam.d/secrets-dispatcher.
// Uses --no-block so it does not hang the login session (PROV-02).
const pamConfigTemplate = `# Managed by secrets-dispatcher provision. Do not edit manually.
# Start/stop companion daemon on desktop user login/logout.
session optional pam_exec.so quiet /usr/bin/systemctl start --no-block secrets-dispatcher-companion@%u.service
`
