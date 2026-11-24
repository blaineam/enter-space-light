#!/bin/sh
# Installs Enter Space Light from the release tarball into ~/.local (no root).
# Packages (.deb/.rpm) are the better choice where they fit your distro.
set -eu
here="$(cd "$(dirname "$0")" && pwd)"
bin="${HOME}/.local/bin"
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
icons="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor"
mkdir -p "$bin" "$apps"
if command -v esl >/dev/null 2>&1 && [ "$(command -v esl)" = "$bin/esl" ]; then
  "$bin/esl" quit 2>/dev/null || true
fi
install -m 0755 "$here/esl" "$bin/esl"
sed "s|^Exec=esl |Exec=$bin/esl |" "$here/enter-space-light.desktop" > "$apps/enter-space-light.desktop"
for s in 16 32 48 64 128 256 512; do
  mkdir -p "$icons/${s}x${s}/apps"
  install -m 0644 "$here/icons/icon-$s.png" "$icons/${s}x${s}/apps/enter-space-light.png"
done
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$apps" || true
if ! command -v fusermount3 >/dev/null 2>&1 && ! command -v fusermount >/dev/null 2>&1; then
  echo "Note: install FUSE to mount drives, e.g.  sudo apt install fuse3  or  sudo dnf install fuse3"
fi
echo "Installed to $bin/esl. Start it from your app menu, or run: esl"
case ":$PATH:" in *":$bin:"*) ;; *) echo "($bin is not on your PATH)";; esac
