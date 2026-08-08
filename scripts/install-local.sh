#!/usr/bin/env bash
# Installs NusaShell from a local repo build into the user's home directory.
#
# Mirrors scripts/install.sh (the curl installer) but sources the app from the
# local electron-forge package output instead of downloading a release tarball.
# This is what `make install` runs after `pnpm desktop:make`.
#
# Durable application state (conversations, AI settings, notes, user plugins)
# lives under Electron userData (prod appData/.../nusashell). This installer
# never reads or writes that directory — only the app binary under
# ~/.local/share/nusashell (Linux) or ~/Applications (macOS).
#
# Usage:
#   make install                      # build + install
#   bash scripts/install-local.sh     # install from an existing build
#   NUSASHELL_BUILD_DIR=out/NusaShell-linux-x64 bash scripts/install-local.sh
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(cat "$repo_root/VERSION" | tr -d '[:space:]')"
home_dir="${HOME:?HOME must be set}"

case "$(uname -s)" in
  Linux) os=linux;;
  Darwin) os=darwin;;
  *) echo "NusaShell supports Linux and macOS only; use install.ps1 on Windows." >&2; exit 1;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=x64;;
  arm64|aarch64) arch=arm64;;
  *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1;;
esac

# Default build dir matches electron-forge package output naming.
build_dir="${NUSASHELL_BUILD_DIR:-$repo_root/apps/desktop/out/NusaShell-${os}-${arch}}"

if [[ ! -d "$build_dir" ]]; then
  echo "Build output not found at: $build_dir" >&2
  echo "Run 'make install' (which builds first) or 'pnpm desktop:make' before this script." >&2
  exit 1
fi

echo "Installing NusaShell $version from local build: $build_dir"

# ---- macOS: copy .app to ~/Applications ----
if [[ "$os" == darwin ]]; then
  app_src="$build_dir/NusaShell.app"
  if [[ ! -d "$app_src" ]]; then
    echo "Expected NusaShell.app inside: $build_dir" >&2
    exit 1
  fi
  mkdir -p "$home_dir/Applications"
  rm -rf "$home_dir/Applications/NusaShell.app"
  cp -R "$app_src" "$home_dir/Applications/NusaShell.app"
  xattr -dr com.apple.quarantine "$home_dir/Applications/NusaShell.app" 2>/dev/null || true
  echo "Installed NusaShell $version in ~/Applications."
  exit 0
fi

# ---- Linux: versioned install under ~/.local/share/nusashell ----
root="$home_dir/.local/share/nusashell"
versions="$root/versions"
current="$root/current"
bin="$home_dir/.local/bin"
mkdir -p "$versions" "$bin" "$home_dir/.local/share/applications"

previous_version=""
if [[ -e "$current" || -L "$current" ]]; then
  previous_target="$(readlink -f "$current" 2>/dev/null || true)"
  if [[ -n "$previous_target" ]]; then previous_version="$(basename "$previous_target")"; fi
fi

target="$versions/$version"
echo "Installing to: $target"
rm -rf "$target"
mkdir -p "$target"
cp -R "$build_dir/." "$target/"

# Prefer Chromium's unprivileged user-namespace sandbox. The setuid helper is
# only a fallback for hosts that explicitly disable user namespaces.
sandbox="$target/chrome-sandbox"
no_sandbox=""
sandbox_ok=0
userns_ok=0
if command -v unshare >/dev/null 2>&1 && unshare -Ur true >/dev/null 2>&1; then
  userns_ok=1
fi
if [[ -e "$sandbox" ]]; then
  mode="$(stat -c '%a' "$sandbox" 2>/dev/null || echo 0)"
  owner="$(stat -c '%u' "$sandbox" 2>/dev/null || echo 1)"
  if [[ "$owner" == "0" && "$mode" == "4755" ]]; then
    sandbox_ok=1
  fi
fi
if [[ "$sandbox_ok" != 1 && "$userns_ok" == 1 ]]; then
  sandbox_ok=1
  if [[ -e "$sandbox" ]]; then mv -f "$sandbox" "$sandbox.disabled"; fi
fi

if [[ "$sandbox_ok" -ne 1 ]]; then
  if [[ -e "$sandbox" ]]; then mv -f "$sandbox" "$sandbox.disabled"; fi
  no_sandbox=" --no-sandbox"
  echo "User namespaces and a root-owned Chromium helper are unavailable; launching with --no-sandbox." >&2
fi

ln -sfn "$target" "$root/.current-$version"
mv -Tf "$root/.current-$version" "$current"

# Clean up old versions (keep current + previous for rollback).
for candidate in "$versions"/*; do
  [[ -d "$candidate" && ! -L "$candidate" ]] || continue
  candidate_version="$(basename "$candidate")"
  if [[ "$candidate_version" != "$version" && "$candidate_version" != "$previous_version" ]]; then
    # Do not delete a version folder while its process is still running —
    # the user may not have quit NusaShell before updating.
    if pgrep -f "$candidate" >/dev/null 2>&1; then
      echo "Keeping old version $candidate_version (process still running)." >&2
    else
      rm -rf "$candidate"
    fi
  fi
done

printf '#!/usr/bin/env sh\nexec "%s/NusaShell"%s "$@"\n' "$current" "$no_sandbox" > "$bin/nusashell"
chmod +x "$bin/nusashell"

cat > "$home_dir/.local/share/applications/nusashell.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=NusaShell
Comment=NusaShell — AI tool shell
Exec=$bin/nusashell
Icon=$current/resources/nusashell.png
Terminal=false
Categories=Utility;Development;
EOF

if [[ ":$PATH:" != *":$bin:"* ]]; then
  echo "Add this to your shell profile: export PATH=\"\$HOME/.local/bin:\$PATH\""
fi
echo "Installed NusaShell $version."

# Detect if NusaShell is still running from a previous version. Electron's
# single-instance lock means launching a new process would just focus the old
# one — the user must quit and relaunch to activate the update.
running_pid=""
if pgrep -f "$root/versions/.*/NusaShell" >/dev/null 2>&1; then
  running_pid="$(pgrep -f "$root/versions/.*/NusaShell" | head -1)"
fi

if [[ -n "$running_pid" ]]; then
  echo "NusaShell is still running (PID $running_pid) from a previous version." >&2
  can_prompt=0
  if [[ "${NUSASHELL_NON_INTERACTIVE:-}" != "1" ]] && { [[ -t 0 ]] || [[ -r /dev/tty ]]; }; then
    can_prompt=1
  fi
  if [[ "$can_prompt" -eq 1 ]]; then
    if [[ -r /dev/tty ]]; then
      printf "Restart NusaShell now to activate the new version? [Y/n] " >/dev/tty
      read -r reply </dev/tty || reply=n
    else
      printf "Restart NusaShell now to activate the new version? [Y/n] " >&2
      read -r reply || reply=n
    fi
    case "${reply:-Y}" in
      Y|y|"")
        echo "Quitting NusaShell (PID $running_pid)..." >&2
        kill "$running_pid" 2>/dev/null || true
        for _ in 1 2 3 4 5; do
          kill -0 "$running_pid" 2>/dev/null || break
          sleep 1
        done
        kill -9 "$running_pid" 2>/dev/null || true
        sleep 1
        echo "Launching NusaShell $version..." >&2
        nohup "$bin/nusashell" >/dev/null 2>&1 &
        echo "NusaShell $version launched."
        exit 0
        ;;
      *)
        echo "Restart NusaShell manually to use the new version: nusashell" >&2
        ;;
    esac
  else
    echo "Restart NusaShell manually to use the new version: nusashell" >&2
  fi
else
  echo "Run: nusashell"
fi
