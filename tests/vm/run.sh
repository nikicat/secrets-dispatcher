#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd -- "$SCRIPT_DIR/../.." && pwd)

DISTRO=${1:-${DISTRO:-ubuntu}}
MODE=${MODE:-local}
BACKEND=${BACKEND:-gnome-keyring}
GNOME_PROFILE=${GNOME_PROFILE:-desktop}
DESKTOP_USER=${DESKTOP_USER:-sdtest}
VM_NAME=${VM_NAME:-secrets-dispatcher-${DISTRO}-${MODE}-${BACKEND}}
VM_DIR=${VM_DIR:-$ROOT_DIR/.vm/$VM_NAME}
VM_IMAGE_CACHE=${VM_IMAGE_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/vm-images}
BINARY=${BINARY:-$ROOT_DIR/secrets-dispatcher}
KEEP_VM=${KEEP_VM:-0}
GOPASS_SOURCE=${GOPASS_SOURCE:-github.com/gopasspw/gopass@latest}
GOPASS_SECRET_SERVICE_SOURCE=${GOPASS_SECRET_SERVICE_SOURCE:-github.com/nikicat/gopass-secret-service/cmd/gopass-secret@latest}
GOPASS_SECRET_SERVICE_BIN=${GOPASS_SECRET_SERVICE_BIN:-/usr/local/bin/gopass-secret-service}

UBUNTU_RELEASE=26.04
UBUNTU_CODENAME=resolute
UBUNTU_IMAGE_URL=${UBUNTU_IMAGE_URL:-https://cloud-images.ubuntu.com/releases/${UBUNTU_RELEASE}/release/ubuntu-${UBUNTU_RELEASE}-server-cloudimg-amd64.img}
FEDORA_RELEASE=44
FEDORA_IMAGE_URL=${FEDORA_IMAGE_URL:-https://download.fedoraproject.org/pub/fedora/linux/releases/${FEDORA_RELEASE}/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-${FEDORA_RELEASE}-1.7.x86_64.qcow2}

case "$DISTRO" in
ubuntu)
	IMAGE_URL=$UBUNTU_IMAGE_URL
	IMAGE_NAME=ubuntu-${UBUNTU_RELEASE}-${UBUNTU_CODENAME}-server-cloudimg-amd64.img
	OS_VARIANT=generic
	;;
fedora)
	IMAGE_URL=$FEDORA_IMAGE_URL
	IMAGE_NAME=fedora-${FEDORA_RELEASE}-cloud-base-generic.x86_64.qcow2
	OS_VARIANT=generic
	;;
*)
	echo "unsupported DISTRO=$DISTRO (want ubuntu or fedora)" >&2
	exit 2
	;;
esac

missing_commands() {
	local cmd
	for cmd in "$@"; do
		if ! command -v "$cmd" >/dev/null 2>&1; then
			printf '%s\n' "$cmd"
		fi
	done
}

host_package_list() {
	local host_id=${1:-}
	case "$host_id" in
	ubuntu|debian)
		printf '%s\n' curl qemu-utils openssh-client libvirt-clients virtinst genisoimage
		;;
	fedora)
		printf '%s\n' curl qemu-img openssh-clients libvirt-client virt-install genisoimage
		;;
	*)
		return 1
		;;
	esac
}

try_install_host_deps() {
	local missing=$1
	local host_id=
	if [ -r /etc/os-release ]; then
		. /etc/os-release
		host_id=${ID:-}
	fi
	if [ -z "$missing" ]; then
		return 0
	fi
	if [ "${VM_INSTALL_HOST_DEPS:-1}" != 1 ]; then
		echo "missing required host commands: $missing" >&2
		return 1
	fi
	case "$host_id" in
	ubuntu|debian)
		local packages
		packages=$(host_package_list "$host_id" | tr '\n' ' ')
		echo "missing host commands: $missing"
		echo "trying to install host VM dependencies with apt: $packages"
		if sudo -n true 2>/dev/null; then
			sudo apt-get update
			sudo apt-get install -y $packages
		else
			echo "sudo needs authentication; run: sudo apt-get update && sudo apt-get install -y $packages" >&2
			return 1
		fi
		;;
	fedora)
		local packages
		packages=$(host_package_list "$host_id" | tr '\n' ' ')
		echo "missing host commands: $missing"
		echo "trying to install host VM dependencies with dnf: $packages"
		if sudo -n true 2>/dev/null; then
			sudo dnf install -y $packages
		else
			echo "sudo needs authentication; run: sudo dnf install -y $packages" >&2
			return 1
		fi
		;;
	*)
		echo "missing required host commands: $missing" >&2
		echo "unsupported host distro for auto-install; install libvirt, virt-install, qemu-img, OpenSSH client tools, curl, and genisoimage manually" >&2
		return 1
		;;
	esac
}

required_commands=(curl qemu-img ssh scp ssh-keygen virsh virt-install)
missing=$(missing_commands "${required_commands[@]}")
try_install_host_deps "$missing"
missing=$(missing_commands "${required_commands[@]}")
if [ -n "$missing" ]; then
	echo "missing required host commands after dependency install attempt: $missing" >&2
	exit 127
fi

if command -v cloud-localds >/dev/null 2>&1; then
	SEED_ISO_TOOL=cloud-localds
elif command -v genisoimage >/dev/null 2>&1; then
	SEED_ISO_TOOL=genisoimage
elif command -v mkisofs >/dev/null 2>&1; then
	SEED_ISO_TOOL=mkisofs
elif command -v xorriso >/dev/null 2>&1; then
	SEED_ISO_TOOL=xorriso
else
	echo "missing seed ISO tool: install cloud-image-utils or provide genisoimage/mkisofs/xorriso" >&2
	exit 127
fi

if [ ! -x "$BINARY" ]; then
	echo "binary not found or not executable: $BINARY" >&2
	echo "run through make vm-test so the binary is built first" >&2
	exit 1
fi

mkdir -p "$VM_DIR" "$VM_IMAGE_CACHE"

SSH_KEY=$VM_DIR/id_ed25519
if [ ! -f "$SSH_KEY" ]; then
	ssh-keygen -q -t ed25519 -N "" -f "$SSH_KEY"
fi
SSH_PUB=$(<"$SSH_KEY.pub")

BASE_IMAGE=$VM_IMAGE_CACHE/$IMAGE_NAME
if [ ! -f "$BASE_IMAGE" ]; then
	echo "downloading $IMAGE_URL"
	curl -fL --retry 3 --output "$BASE_IMAGE.partial" "$IMAGE_URL"
	mv "$BASE_IMAGE.partial" "$BASE_IMAGE"
fi

DISK=$VM_DIR/disk.qcow2
SEED=$VM_DIR/seed.iso
USER_DATA=$VM_DIR/user-data
META_DATA=$VM_DIR/meta-data

cat >"$USER_DATA" <<EOF_USER_DATA
#cloud-config
users:
  - name: tester
    gecos: VM Test User
    groups: [adm, wheel, sudo]
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    lock_passwd: true
    ssh_authorized_keys:
      - $SSH_PUB
ssh_pwauth: false
disable_root: true
package_update: false
final_message: secrets-dispatcher VM ready
EOF_USER_DATA

cat >"$META_DATA" <<EOF_META_DATA
instance-id: $VM_NAME
local-hostname: $VM_NAME
EOF_META_DATA

cleanup_existing_vm() {
	if virsh dominfo "$VM_NAME" >/dev/null 2>&1; then
		virsh destroy "$VM_NAME" >/dev/null 2>&1 || true
		virsh undefine "$VM_NAME" --nvram --remove-all-storage >/dev/null 2>&1 || virsh undefine "$VM_NAME" >/dev/null 2>&1 || true
	fi
}

cleanup() {
	if [ "$KEEP_VM" = 1 ]; then
		echo "KEEP_VM=1, leaving VM running: $VM_NAME"
		return
	fi
	cleanup_existing_vm
}
trap cleanup EXIT

cleanup_existing_vm
rm -f "$DISK" "$SEED"
qemu-img convert -q -O qcow2 "$BASE_IMAGE" "$DISK"
qemu-img resize -q "$DISK" 40G
case "$SEED_ISO_TOOL" in
cloud-localds)
	cloud-localds "$SEED" "$USER_DATA" "$META_DATA"
	;;
genisoimage|mkisofs)
	"$SEED_ISO_TOOL" -quiet -output "$SEED" -volid cidata -joliet -rock "$USER_DATA" "$META_DATA"
	;;
xorriso)
	xorriso -as mkisofs -quiet -output "$SEED" -volid cidata -joliet -rock "$USER_DATA" "$META_DATA"
	;;
esac

virsh net-start default >/dev/null 2>&1 || true
virsh net-autostart default >/dev/null 2>&1 || true

echo "starting VM $VM_NAME ($DISTRO, mode=$MODE, gnome_profile=$GNOME_PROFILE)"
virt-install \
	--name "$VM_NAME" \
	--memory 4096 \
	--vcpus 2 \
	--import \
	--disk "path=$DISK,format=qcow2,bus=virtio" \
	--disk "path=$SEED,device=cdrom" \
	--network network=default,model=virtio \
	--graphics none \
	--noautoconsole \
	--os-variant "$OS_VARIANT"

vm_ip() {
	virsh domifaddr "$VM_NAME" --source lease 2>/dev/null | awk '/ipv4/ { sub("/.*", "", $4); print $4; exit }'
}

echo "waiting for VM DHCP lease"
IP=""
for _ in $(seq 1 120); do
	IP=$(vm_ip || true)
	if [ -n "$IP" ]; then
		break
	fi
	sleep 2
done
if [ -z "$IP" ]; then
	echo "timed out waiting for VM IP" >&2
	virsh domifaddr "$VM_NAME" || true
	exit 1
fi

SSH_OPTS=(
	-o StrictHostKeyChecking=no
	-o UserKnownHostsFile=/dev/null
	-o ConnectTimeout=5
	-i "$SSH_KEY"
)

echo "waiting for SSH at $IP"
for _ in $(seq 1 120); do
	if ssh "${SSH_OPTS[@]}" "tester@$IP" true >/dev/null 2>&1; then
		break
	fi
	sleep 2
done
ssh "${SSH_OPTS[@]}" "tester@$IP" true

echo "copying binary and guest test script"
scp "${SSH_OPTS[@]}" "$BINARY" "tester@$IP:/tmp/secrets-dispatcher"
scp "${SSH_OPTS[@]}" "$SCRIPT_DIR/guest-smoke.sh" "tester@$IP:/tmp/guest-smoke.sh"

ssh "${SSH_OPTS[@]}" "tester@$IP" \
	"sudo install -m 0755 /tmp/secrets-dispatcher /usr/local/bin/secrets-dispatcher && sudo chmod 0755 /tmp/guest-smoke.sh"

echo "running guest smoke test"
ssh "${SSH_OPTS[@]}" "tester@$IP" \
	"sudo env DISTRO='$DISTRO' MODE='$MODE' BACKEND='$BACKEND' GNOME_PROFILE='$GNOME_PROFILE' DESKTOP_USER='$DESKTOP_USER' GOPASS_SOURCE='$GOPASS_SOURCE' GOPASS_SECRET_SERVICE_SOURCE='$GOPASS_SECRET_SERVICE_SOURCE' GOPASS_SECRET_SERVICE_BIN='$GOPASS_SECRET_SERVICE_BIN' /tmp/guest-smoke.sh"

echo "VM smoke test passed: distro=$DISTRO mode=$MODE backend=$BACKEND"
