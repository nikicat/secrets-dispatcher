#!/usr/bin/env bash
set -euo pipefail

DISTRO=${DISTRO:-unknown}
MODE=${MODE:-local}
BACKEND=${BACKEND:-gnome-keyring}
GNOME_PROFILE=${GNOME_PROFILE:-desktop}
DESKTOP_USER=${DESKTOP_USER:-sdtest}
BINARY=${BINARY:-/usr/local/bin/secrets-dispatcher}
GOPASS_SOURCE=${GOPASS_SOURCE:-github.com/gopasspw/gopass@latest}
GOPASS_SECRET_SERVICE_SOURCE=${GOPASS_SECRET_SERVICE_SOURCE:-github.com/nikicat/gopass-secret-service/cmd/gopass-secret@latest}
GOPASS_SECRET_SERVICE_BIN=${GOPASS_SECRET_SERVICE_BIN:-/usr/local/bin/gopass-secret-service}

export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin

log() {
	printf '[vm-smoke] %s\n' "$*"
}

install_packages_ubuntu() {
	export DEBIAN_FRONTEND=noninteractive
	apt-get update
	apt-get install -y --no-install-recommends \
		ca-certificates curl sudo dbus dbus-user-session dbus-x11 \
		gnome-keyring libsecret-tools systemd-container procps
	if [ "$BACKEND" = gopass ]; then
		apt-get install -y --no-install-recommends gnupg git golang-go
		apt-get install -y --no-install-recommends gopass || true
	fi
	if [ "$GNOME_PROFILE" = desktop ]; then
		apt-get install -y --no-install-recommends ubuntu-desktop-minimal || \
			apt-get install -y --no-install-recommends gnome-session gdm3
	fi
}

install_packages_fedora() {
	dnf install -y \
		ca-certificates curl sudo dbus-daemon dbus-tools \
		gnome-keyring libsecret systemd-container procps-ng shadow-utils
	if [ "$BACKEND" = gopass ]; then
		dnf install -y gnupg2 git golang
		dnf install -y gopass || true
	fi
	if [ "$GNOME_PROFILE" = desktop ]; then
		if ! dnf group install -y "GNOME Desktop Environment"; then
			log "GNOME Desktop Environment group install failed, trying core GNOME packages"
			dnf install -y gnome-shell gnome-session gdm || true
		fi
	fi
}

install_packages() {
	case "$DISTRO" in
	ubuntu) install_packages_ubuntu ;;
	fedora) install_packages_fedora ;;
	*)
		. /etc/os-release
		case "${ID:-}" in
		ubuntu|debian) install_packages_ubuntu ;;
		fedora) install_packages_fedora ;;
		*) echo "unsupported guest distro: ${ID:-unknown}" >&2; exit 2 ;;
		esac
		;;
	esac
}

ensure_desktop_user() {
	if ! id "$DESKTOP_USER" >/dev/null 2>&1; then
		useradd -m -s /bin/bash "$DESKTOP_USER"
	fi
	if command -v usermod >/dev/null 2>&1; then
		usermod -aG sudo "$DESKTOP_USER" >/dev/null 2>&1 || true
		usermod -aG wheel "$DESKTOP_USER" >/dev/null 2>&1 || true
	fi
}

desktop_uid() {
	id -u "$DESKTOP_USER"
}

run_as_desktop() {
	local uid home
	uid=$(desktop_uid)
	home="/home/$DESKTOP_USER"
	(
		cd "$home"
		runuser -u "$DESKTOP_USER" -- env -i \
			HOME="$home" \
			USER="$DESKTOP_USER" \
			LOGNAME="$DESKTOP_USER" \
			SHELL=/bin/bash \
			PATH="$PATH" \
			GOPASS_NO_NOTIFY=true \
			XDG_RUNTIME_DIR="/run/user/$uid" \
			DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$uid/bus" \
			"$@"
	)
}

secret_service_has_owner() {
	run_as_desktop busctl --user --timeout=5 call \
		org.freedesktop.DBus /org/freedesktop/DBus org.freedesktop.DBus NameHasOwner \
		s org.freedesktop.secrets 2>/dev/null | grep -q 'b true'
}

wait_for_secret_service_owner() {
	for _ in $(seq 1 60); do
		if secret_service_has_owner; then
			return 0
		fi
		sleep 1
	done
	log "org.freedesktop.secrets owner did not appear"
	run_as_desktop systemctl --user status secrets-dispatcher.service --no-pager || true
	run_as_desktop journalctl --user -u secrets-dispatcher.service --no-pager -n 100 || true
	run_as_desktop busctl --user --no-pager list || true
	return 1
}

install_go_tool_if_missing() {
	local command_name=$1
	local source=$2
	if command -v "$command_name" >/dev/null 2>&1; then
		return 0
	fi
	log "installing $source with go install"
	GOBIN=/usr/local/bin go install "$source"
	if ! command -v "$command_name" >/dev/null 2>&1; then
		echo "$command_name not found after installing $source" >&2
		exit 1
	fi
}

install_gopass_secret_service() {
	if [ -x "$GOPASS_SECRET_SERVICE_BIN" ]; then
		return 0
	fi
	if command -v gopass-secret-service >/dev/null 2>&1; then
		GOPASS_SECRET_SERVICE_BIN=$(command -v gopass-secret-service)
		return 0
	fi
	install_go_tool_if_missing gopass-secret "$GOPASS_SECRET_SERVICE_SOURCE"

	local service_command
	service_command=$(command -v gopass-secret)
	mkdir -p "$(dirname -- "$GOPASS_SECRET_SERVICE_BIN")"
	cat >"$GOPASS_SECRET_SERVICE_BIN" <<EOF_WRAPPER
#!/usr/bin/env bash
exec "$service_command" service "\$@"
EOF_WRAPPER
	chmod 0755 "$GOPASS_SECRET_SERVICE_BIN"
}

prepare_gopass_backend() {
	log "preparing disposable gopass backend"
	install_go_tool_if_missing gopass "$GOPASS_SOURCE"
	install_gopass_secret_service
	if [ ! -x "$GOPASS_SECRET_SERVICE_BIN" ]; then
		if command -v gopass-secret-service >/dev/null 2>&1; then
			GOPASS_SECRET_SERVICE_BIN=$(command -v gopass-secret-service)
		else
			echo "gopass-secret-service not found after install" >&2
			exit 1
		fi
	fi

	run_as_desktop mkdir -p "/home/$DESKTOP_USER/.gnupg" "/home/$DESKTOP_USER/.config/gopass"
	run_as_desktop chmod 0700 "/home/$DESKTOP_USER/.gnupg" "/home/$DESKTOP_USER/.config/gopass"

	local email key_file fingerprint
	email="$DESKTOP_USER@secrets-dispatcher.invalid"
	key_file=/tmp/secrets-dispatcher-gopass-key.batch
	cat >"$key_file" <<EOF_KEY
%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: Secrets Dispatcher VM
Name-Email: $email
Expire-Date: 0
%commit
EOF_KEY
	chown "$DESKTOP_USER:$DESKTOP_USER" "$key_file"
	chmod 0600 "$key_file"
	if ! run_as_desktop gpg --batch --list-secret-keys "$email" >/dev/null 2>&1; then
		run_as_desktop gpg --batch --generate-key "$key_file"
	fi
	fingerprint=$(run_as_desktop gpg --batch --with-colons --list-secret-keys "$email" | awk -F: '/^fpr:/ { print $10; exit }')
	if [ -z "$fingerprint" ]; then
		echo "failed to determine generated GPG key fingerprint" >&2
		exit 1
	fi
	run_as_desktop git config --global user.email "$email"
	run_as_desktop git config --global user.name "Secrets Dispatcher VM"

	if ! run_as_desktop gopass stores 2>/dev/null | grep -q '^root'; then
		if ! run_as_desktop timeout 120s gopass --yes init "$fingerprint"; then
			run_as_desktop timeout 120s gopass init "$fingerprint"
		fi
	fi

	verify_gopass_secret_service_direct
}

verify_gopass_secret_service_direct() {
	log "verifying gopass-secret-service directly with secret-tool"
	cat >/tmp/gopass-secret-service-direct-check.sh <<'EOF_DIRECT'
#!/usr/bin/env bash
set -euo pipefail
service_bin=$1
log_file=${TMPDIR:-/tmp}/gopass-secret-service-direct.log
"$service_bin" >"$log_file" 2>&1 &
service_pid=$!
cleanup() {
	local status=$?
	if [ "$status" -ne 0 ]; then
		printf 'gopass-secret-service direct-check log:\n' >&2
		cat "$log_file" >&2 || true
	fi
	kill "$service_pid" >/dev/null 2>&1 || true
	wait "$service_pid" >/dev/null 2>&1 || true
}
trap cleanup EXIT
for _ in $(seq 1 100); do
	if busctl --user --timeout=2 call org.freedesktop.DBus /org/freedesktop/DBus org.freedesktop.DBus NameHasOwner s org.freedesktop.secrets 2>/dev/null | grep -q 'b true'; then
		break
	fi
	if ! kill -0 "$service_pid" >/dev/null 2>&1; then
		cat "$log_file" >&2 || true
		exit 1
	fi
	sleep 0.1
done
busctl --user --timeout=5 call org.freedesktop.DBus /org/freedesktop/DBus org.freedesktop.DBus NameHasOwner s org.freedesktop.secrets | grep -q 'b true'
for _ in $(seq 1 100); do
	if busctl --user --timeout=2 call org.freedesktop.secrets /org/freedesktop/secrets/aliases/default org.freedesktop.DBus.Properties GetAll s org.freedesktop.Secret.Collection >/dev/null 2>&1; then
		break
	fi
	if ! kill -0 "$service_pid" >/dev/null 2>&1; then
		cat "$log_file" >&2 || true
		exit 1
	fi
	sleep 0.1
done
busctl --user --timeout=5 call org.freedesktop.secrets /org/freedesktop/secrets/aliases/default org.freedesktop.DBus.Properties GetAll s org.freedesktop.Secret.Collection >/dev/null
printf 'vm-secret\n' | secret-tool store --label='VM gopass check' vm-backend gopass
got=$(secret-tool lookup vm-backend gopass)
if [ "$got" != vm-secret ]; then
	echo "secret-tool lookup returned '$got'" >&2
	exit 1
fi
EOF_DIRECT
	chmod 0755 /tmp/gopass-secret-service-direct-check.sh
	run_as_desktop dbus-run-session -- /tmp/gopass-secret-service-direct-check.sh "$GOPASS_SECRET_SERVICE_BIN"
}

start_user_session_bus() {
	local uid runtime bus
	uid=$(desktop_uid)
	runtime=/run/user/$uid
	bus=$runtime/bus
	loginctl enable-linger "$DESKTOP_USER" || true
	systemctl start "user@$uid.service"
	for _ in $(seq 1 60); do
		if [ -S "$bus" ]; then
			return 0
		fi
		sleep 1
	done
	log "user manager status follows"
	systemctl status "user@$uid.service" --no-pager || true
	return 1
}

wait_for_user_unit() {
	local unit=$1
	for _ in $(seq 1 60); do
		if run_as_desktop systemctl --user is-active --quiet "$unit"; then
			return 0
		fi
		sleep 1
	done
	run_as_desktop systemctl --user status "$unit" --no-pager || true
	return 1
}

assert_api_requires_auth() {
	local code
	code=$(run_as_desktop curl -sS -o /tmp/secrets-dispatcher-status.json -w '%{http_code}' http://127.0.0.1:8484/api/v1/status || true)
	if [ "$code" != 401 ]; then
		log "unexpected status API response code: $code"
		cat /tmp/secrets-dispatcher-status.json 2>/dev/null || true
		return 1
	fi
}

install_local_mode() {
	local backend_args=()
	case "$BACKEND" in
	gnome-keyring)
		backend_args=(--backend gnome-keyring)
		;;
	gopass)
		backend_args=(--backend "$GOPASS_SECRET_SERVICE_BIN")
		;;
	*)
		echo "unsupported BACKEND=$BACKEND (want gnome-keyring or gopass)" >&2
		exit 2
		;;
	esac
	log "installing service mode=$MODE backend=$BACKEND"
	run_as_desktop "$BINARY" service install --mode "$MODE" "${backend_args[@]}"
	run_as_desktop systemctl --user start secrets-dispatcher.service
	wait_for_user_unit secrets-dispatcher.service
	wait_for_secret_service_owner
	assert_api_requires_auth
}

install_secure_local_mode() {
	if [ "$BACKEND" != gnome-keyring ]; then
		echo "secure-local currently supports BACKEND=gnome-keyring only" >&2
		exit 2
	fi
	log "installing secure-local mode"
	"$BINARY" provision --mode secure-local --user "$DESKTOP_USER"
	run_as_desktop "$BINARY" service install --mode secure-local
	systemctl enable --now "secrets-dispatcher-secure@$DESKTOP_USER.service"
	for _ in $(seq 1 60); do
		if systemctl is-active --quiet "secrets-dispatcher-secure@$DESKTOP_USER.service"; then
			break
		fi
		sleep 1
	done
	systemctl is-active --quiet "secrets-dispatcher-secure@$DESKTOP_USER.service"
	wait_for_secret_service_owner
}

log "guest distro: $(. /etc/os-release && printf '%s %s' "${NAME:-unknown}" "${VERSION_ID:-unknown}")"
log "mode=$MODE backend=$BACKEND gnome_profile=$GNOME_PROFILE desktop_user=$DESKTOP_USER"

install_packages
ensure_desktop_user
start_user_session_bus

if [ "$BACKEND" = gopass ]; then
	prepare_gopass_backend
fi

"$BINARY" version >/dev/null

case "$MODE" in
local|full) install_local_mode ;;
secure-local) install_secure_local_mode ;;
*) echo "unsupported MODE=$MODE for VM smoke test" >&2; exit 2 ;;
esac

log "smoke test complete"
