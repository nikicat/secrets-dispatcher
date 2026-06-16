# VM E2E Harness

This harness boots a disposable libvirt/QEMU VM, installs the GNOME Secret Service stack, copies the locally built `secrets-dispatcher` binary into the guest, runs `service install`, and checks that the installed service owns `org.freedesktop.secrets` on a real user session bus.

The default guest targets are:

- Ubuntu 26.04 LTS (Resolute Raccoon), hardcoded as the current latest Ubuntu LTS.
- Fedora 44 Cloud, the current latest Fedora Cloud release.

The harness is host-driven and should work from either Ubuntu or Fedora hosts with libvirt installed.

## Host Dependencies

The runner checks for required host commands and tries to install missing dependencies automatically on Ubuntu/Debian and Fedora hosts when passwordless `sudo` is available. Set `VM_INSTALL_HOST_DEPS=0` to disable that behavior.

Ubuntu host:

```bash
sudo apt install qemu-kvm libvirt-daemon-system virtinst cloud-image-utils qemu-utils openssh-client curl
sudo usermod -aG libvirt,kvm "$USER"
```

Fedora host:

```bash
sudo dnf install @virtualization virt-install libvirt-daemon-kvm cloud-utils qemu-img openssh-clients curl
sudo systemctl enable --now libvirtd
sudo usermod -aG libvirt "$USER"
```

Log out and back in after changing group membership.

## Usage

Run Ubuntu 26.04 LTS:

```bash
make vm-test-ubuntu
```

Run Fedora 44:

```bash
make vm-test-fedora
```

Run the same smoke test with a real gopass backend:

```bash
make vm-test-ubuntu-gopass
make vm-test-fedora-gopass
```

Or choose explicitly:

```bash
make vm-test DISTRO=ubuntu MODE=local
make vm-test DISTRO=fedora MODE=local
make vm-test DISTRO=ubuntu MODE=local BACKEND=gopass
```

Useful environment variables:

- `DISTRO`: `ubuntu` or `fedora`.
- `MODE`: install mode passed to `secrets-dispatcher service install`; defaults to `local` on this branch.
- `BACKEND`: `gnome-keyring` or `gopass`; defaults to `gnome-keyring`.
- `GNOME_PROFILE`: `desktop` installs fuller GNOME packages when available; `minimal` installs only the Secret Service/session-bus packages. Defaults to `desktop`.
- `KEEP_VM=1`: leave the VM running for debugging.
- `VM_NAME`: override the libvirt domain name.
- `VM_DIR`: override the per-run working directory; defaults to `.vm/<name>`.
- `VM_IMAGE_CACHE`: override downloaded cloud-image cache; defaults to `${XDG_CACHE_HOME:-~/.cache}/secrets-dispatcher/vm-images`.
- `UBUNTU_IMAGE_URL`: override the Ubuntu image URL.
- `FEDORA_IMAGE_URL`: override the Fedora image URL.
- `VM_INSTALL_HOST_DEPS`: `1` tries to install missing host commands with `apt-get` or `dnf`; `0` only reports missing dependencies. Defaults to `1`.
- `GOPASS_SOURCE`: Go package used when `gopass` is not available from distro packages; defaults to `github.com/gopasspw/gopass@latest`.
- `GOPASS_SECRET_SERVICE_SOURCE`: Go package used when `gopass-secret-service` is not available; defaults to `github.com/nikicat/gopass-secret-service/cmd/gopass-secret@latest`.
- `GOPASS_SECRET_SERVICE_BIN`: backend command path used for `BACKEND=gopass`; defaults to `/usr/local/bin/gopass-secret-service`. When building from source, the harness creates this wrapper around upstream's `gopass-secret service` command.

## Scope

Current checks are a deployment smoke test, not a full approval-flow test:

- Boot a fresh VM.
- Install GNOME Keyring, Secret Service tools, D-Bus/session packages, and optional desktop packages.
- Create a test desktop user.
- Start the user's systemd user manager and session bus.
- For `BACKEND=gnome-keyring`, install `secrets-dispatcher` in `local` mode with GNOME Keyring.
- For `BACKEND=gopass`, create a disposable no-passphrase GPG key and gopass store, install `gopass-secret-service`, and first verify it directly with `secret-tool` under `dbus-run-session`.
- Start the user units.
- Assert `org.freedesktop.secrets` has an owner on the user bus.
- Assert the Web/API endpoint is reachable and requires auth.

On the secure-local branch, set `MODE=secure-local` to exercise that install path once the branch is checked out. The current `fix/hidden-local-backend` branch does not contain secure-local code.
