#!/usr/bin/env bash
# Installs a signed-by-checksum NusaShell release without requiring root.
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
target="$versions/$resolved_version"
if [[ ! -d "$target" ]]; then mkdir -p "$target"; tar -xzf "$tmp_dir/$file_name" -C "$target" --strip-components=1; fi
no_sandbox=""
if [[ "$(cat /proc/sys/kernel/unprivileged_userns_clone 2>/dev/null || echo 0)" != 1 ]] || [[ "$(cat /proc/sys/user/max_user_namespaces 2>/dev/null || echo 0)" == 0 ]]; then
  no_sandbox=" --no-sandbox"
  echo "Notice: unprivileged user namespaces are disabled; NusaShell will use --no-sandbox. Enable kernel.unprivileged_userns_clone=1 to remove this fallback." >&2
fi
ln -s "$target" "$root/.current-$resolved_version"; mv -f "$root/.current-$resolved_version" "$current"
printf '#!/usr/bin/env sh\nexec "%s/NusaShell"%s "$@"\n' "$current" "$no_sandbox" > "$bin/nusashell"; chmod +x "$bin/nusashell"
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
