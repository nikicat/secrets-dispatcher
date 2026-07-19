#!/usr/bin/env bash
# Runs INSIDE the Tier-2 VM (scp'd in by demo.sh). Stages the desktop *look*
# for recording: dark theme, branded wallpaper, a large cursor, a big terminal
# font, and a neutral user@home prompt. Also disables the screensaver, which
# otherwise puts gnome-shell's KeyringPrompter into a dummy state that cancels
# the unlock dialog instantly (see demo-recording-harness memory).
#
# Self-contained (no host-side variables) so it stays lintable as a real file.
set -euo pipefail

# Screensaver OFF — required for the keyring unlock dialog to render at all.
gsettings set org.gnome.desktop.session idle-delay 0
gsettings set org.gnome.desktop.screensaver lock-enabled false

# Dark, high-contrast look.
gsettings set org.gnome.desktop.interface color-scheme prefer-dark

# Keep GNOME's own animations on — the notification slide-in is part of the
# demo (default is on, but be explicit).
gsettings set org.gnome.desktop.interface enable-animations true

# A large cursor so its movement reads clearly on camera.
gsettings set org.gnome.desktop.interface cursor-size 64

# Branded, *colourful* wallpaper: the mono "greyscale" crest is the technical
# default but reads as black-and-white on camera. Prefer the release mascot
# illustration (Noble Numbat etc.), then any community "*_by_*" art, then the
# generic fallbacks. `gsettings reset` is no good here — it yields GNOME's
# upstream adwaita default, whose file isn't installed on the desktop base,
# leaving a flat navy color.
for wp in \
    /usr/share/backgrounds/*[Nn]umbat*boy*.png \
    /usr/share/backgrounds/*_by_*.png \
    /usr/share/backgrounds/*_by_*.jpg \
    /usr/share/backgrounds/ubuntu-wallpaper-*.png \
    /usr/share/backgrounds/warty-final-ubuntu.png; do
    [[ -e "$wp" ]] || continue
    gsettings set org.gnome.desktop.background picture-uri "file://$wp"
    gsettings set org.gnome.desktop.background picture-uri-dark "file://$wp"
    break
done

# Ubuntu dock: left-anchored, full height, always visible — the classic Ubuntu
# layout (the extension otherwise defaults to a floating dock at the bottom).
# Its schema exists because prep_common apt-installs gnome-shell-extension-
# ubuntu-dock before running this script.
dock=org.gnome.shell.extensions.dash-to-dock
gsettings set "$dock" dock-position LEFT
gsettings set "$dock" extend-height true
gsettings set "$dock" dock-fixed true
gsettings set "$dock" transparency-mode FIXED
# A few real favourites so the dock isn't nearly empty (the dock silently skips
# any that aren't installed).
gsettings set org.gnome.shell favorite-apps \
    "['org.gnome.Nautilus.desktop', 'org.gnome.Terminal.desktop', 'org.gnome.TextEditor.desktop', 'org.gnome.Settings.desktop', 'gnome-control-center.desktop', 'org.gnome.Calculator.desktop']"

# Big, readable terminal text: gnome-terminal ignores the interface
# monospace-font-name while use-system-font is true, so set the profile font.
p=$(gsettings get org.gnome.Terminal.ProfilesList default | tr -d "'")
base="org.gnome.Terminal.Legacy.Profile:/org/gnome/terminal/legacy/profiles:/:$p/"
gsettings set "$base" use-system-font false
gsettings set "$base" font 'Monospace 16'
gsettings set "$base" use-theme-colors false
gsettings set "$base" background-color '#171421'
gsettings set "$base" foreground-color '#D0CFCC'
gsettings set "$base" palette "['#171421', '#C01C28', '#26A269', '#A2734C', '#12488B', '#A347BA', '#2AA1B3', '#D0CFCC', '#5E5C64', '#F66151', '#33DA7A', '#E9AD0C', '#2A7BDE', '#C061CB', '#33C7DE', '#FFFFFF']"

# A neutral prompt (user@home) instead of e2e@gnome-e2e, applied to demo shells.
grep -q 'PS1=.demo' ~/.bashrc 2>/dev/null ||
    printf '\n# demo prompt\nPS1='\''\[\e[1;32m\]user@home\[\e[0m\]:\[\e[1;34m\]~\[\e[0m\]$ '\'' # demo\n' >>~/.bashrc

touch ~/.sudo_as_admin_successful
