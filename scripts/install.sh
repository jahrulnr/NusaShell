#!/usr/bin/env bash
# Installs a signed-by-checksum NusaShell release without requiring root by default.
# On Linux, chrome-sandbox needs root:4755 for Chromium's setuid helper. If that
# cannot be set, the installer disables the helper and launches with --no-sandbox
# — and it does so before claiming success.
set -euo pipefail

repo="${NUSASHELL_REPOSITORY:-jahrulnr/NusaShell}"
base="${NUSASHELL_RELEASE_BASE:-https://github.com/${repo}/releases}"
version="${NUSASHELL_VERSION:-}"
home_dir="${HOME:?HOME must be set}"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/nusashell.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

download() { command -v curl >/dev/null 2>&1 && curl --fail --location --silent --show-error "$1" -o "$2" || wget -qO "$2" "$1"; }
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) echo "NusaShell supports Linux and macOS only; use install.ps1 on Windows." >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) arch=x64;; arm64|aarch64) arch=arm64;; *) echo "Unsupported CPU architecture: $(uname -m)" >&2; exit 1;; esac

manifest_url="${base}/latest/download/latest.json"
if [[ -n "$version" ]]; then manifest_url="${base}/download/v${version}/latest.json"; fi
download "$manifest_url" "$tmp_dir/latest.json" || { echo "No published NusaShell release is available yet." >&2; exit 1; }

read_json() { node -e 'const f=require("fs"); const d=JSON.parse(f.readFileSync(process.argv[1],"utf8")); if(process.argv[3]==="version"){console.log(d.version);process.exit(0)} const x=d.files[process.argv[2]]; if (!x) process.exit(2); console.log(x[process.argv[3]]);' "$tmp_dir/latest.json" "$os-$arch" "$1"; }
resolved_version="$(read_json version)" || { echo "No release payload for $os-$arch." >&2; exit 1; }
file_name="$(read_json name)"
expected_sha="$(read_json sha256)"
download "${base}/download/v${resolved_version}/${file_name}" "$tmp_dir/$file_name"
if [[ "$os" == linux ]]; then actual_sha="$(sha256sum "$tmp_dir/$file_name" | awk '{print $1}')"; else actual_sha="$(shasum -a 256 "$tmp_dir/$file_name" | awk '{print $1}')"; fi
[[ "$actual_sha" == "$expected_sha" ]] || { echo "Checksum verification failed; refusing to install." >&2; exit 1; }

if [[ "$os" == darwin ]]; then
  mkdir -p "$home_dir/Applications"
  unzip -q "$tmp_dir/$file_name" -d "$tmp_dir/unpacked"
  rm -rf "$home_dir/Applications/NusaShell.app"
  mv "$tmp_dir/unpacked/NusaShell.app" "$home_dir/Applications/NusaShell.app"
  xattr -dr com.apple.quarantine "$home_dir/Applications/NusaShell.app" 2>/dev/null || true
  echo "Installed NusaShell $resolved_version in ~/Applications."
  exit 0
fi

root="$home_dir/.local/share/nusashell"; versions="$root/versions"; current="$root/current"; bin="$home_dir/.local/bin"
mkdir -p "$versions" "$bin" "$home_dir/.local/share/applications"
previous_version=""
if [[ -e "$current" || -L "$current" ]]; then
  previous_target="$(readlink -f "$current" 2>/dev/null || true)"
  if [[ -n "$previous_target" ]]; then previous_version="$(basename "$previous_target")"; fi
fi
target="$versions/$resolved_version"
if [[ ! -d "$target/NusaShell" && ! -x "$target/NusaShell" ]]; then
  mkdir -p "$target"
  tar -xzf "$tmp_dir/$file_name" -C "$target" --strip-components=1
fi

# Chromium aborts if chrome-sandbox exists but is not root-owned mode 4755 —
# even when unprivileged user namespaces are enabled. Handle this before success.
sandbox="$target/chrome-sandbox"
no_sandbox=""
sandbox_ok=0
if [[ -e "$sandbox" ]]; then
  mode="$(stat -c '%a' "$sandbox" 2>/dev/null || echo 0)"
  owner="$(stat -c '%u' "$sandbox" 2>/dev/null || echo 1)"
  if [[ "$owner" == "0" && "$mode" == "4755" ]]; then
    sandbox_ok=1
  fi
else
  # No helper binary: Chromium will use userns or fail later; still prefer --no-sandbox
  # only when userns is unavailable.
  if [[ "$(cat /proc/sys/kernel/unprivileged_userns_clone 2>/dev/null || echo 0)" == 1 ]] \
    && [[ "$(cat /proc/sys/user/max_user_namespaces 2>/dev/null || echo 0)" != 0 ]]; then
    sandbox_ok=1
  fi
fi

if [[ "$sandbox_ok" -ne 1 ]]; then
  echo "" >&2
  echo "Chromium sandbox helper needs a one-time root fix before NusaShell can start:" >&2
  echo "  sudo chown root:root \"$sandbox\"" >&2
  echo "  sudo chmod 4755 \"$sandbox\"" >&2
  echo "" >&2

  applied=0
  can_prompt=0
  if [[ "${NUSASHELL_NON_INTERACTIVE:-}" != "1" ]] && { [[ -t 0 ]] || [[ -r /dev/tty ]]; }; then
    can_prompt=1
  fi

  if [[ "$can_prompt" -eq 1 ]]; then
    if [[ -r /dev/tty ]]; then
      printf "Apply that fix with sudo now? [Y/n] " >/dev/tty
      read -r reply </dev/tty || reply=n
    else
      printf "Apply that fix with sudo now? [Y/n] " >&2
      read -r reply || reply=n
    fi
    case "${reply:-Y}" in
      Y|y|"")
        if command -v sudo >/dev/null 2>&1 \
          && sudo chown root:root "$sandbox" \
          && sudo chmod 4755 "$sandbox"; then
          applied=1
          echo "Sandbox helper configured (root:4755)." >&2
        else
          echo "sudo failed; falling back to --no-sandbox." >&2
        fi
        ;;
      *)
        echo "Skipping sudo; falling back to --no-sandbox." >&2
        ;;
    esac
  else
    echo "Non-interactive install: falling back to --no-sandbox (setuid not applied)." >&2
  fi

  if [[ "$applied" -ne 1 ]]; then
    # Misconfigured helper causes FATAL abort — rename it away, then force --no-sandbox.
    if [[ -e "$sandbox" ]]; then
      mv -f "$sandbox" "$sandbox.disabled"
    fi
    no_sandbox=" --no-sandbox"
    echo "Launch wrapper will use --no-sandbox. To restore the real sandbox later:" >&2
    echo "  sudo chown root:root \"$sandbox.disabled\" && sudo chmod 4755 \"$sandbox.disabled\" && mv \"$sandbox.disabled\" \"$sandbox\"" >&2
    echo "  then re-run this installer to rewrite the launcher without --no-sandbox." >&2
  fi
fi

ln -sfn "$target" "$root/.current-$resolved_version"
# Without -T, GNU mv follows the existing directory symlink and moves the
# candidate inside the old version instead of replacing `current`.
mv -Tf "$root/.current-$resolved_version" "$current"
for candidate in "$versions"/*; do
  [[ -d "$candidate" && ! -L "$candidate" ]] || continue
  candidate_version="$(basename "$candidate")"
  if [[ "$candidate_version" != "$resolved_version" && "$candidate_version" != "$previous_version" ]]; then
    rm -rf "$candidate"
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
if [[ ":$PATH:" != *":$bin:"* ]]; then echo "Add this to your shell profile: export PATH=\"\$HOME/.local/bin:\$PATH\""; fi
echo "Installed NusaShell $resolved_version. Run: nusashell"
