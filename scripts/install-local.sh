#!/usr/bin/env bash
# Install the local Electron wrapper produced by `make electron-package`.
# This is the checkout equivalent of the optional Electron part of install.sh.
set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(tr -d '[:space:]' < "$repo_root/apps/electron/VERSION")"
home_dir="${HOME:?HOME must be set}"
build_dir="${NUSASHELL_BUILD_DIR:-}"
semver_re='^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$'
[[ "$version" =~ $semver_re ]] || { echo "Invalid apps/electron/VERSION: $version" >&2; exit 1; }

case "$(uname -s)" in
  Linux)
    os=linux
    default_build_dir="$repo_root/apps/electron/dist/linux-unpacked"
    ;;
  Darwin)
    os=darwin
    if [[ -d "$repo_root/apps/electron/dist/mac" ]]; then
      default_build_dir="$repo_root/apps/electron/dist/mac"
    elif [[ -d "$repo_root/apps/electron/dist/mac-arm64" ]]; then
      default_build_dir="$repo_root/apps/electron/dist/mac-arm64"
    else
      default_build_dir="$repo_root/apps/electron/dist/mac-x64"
    fi
    ;;
  *)
    echo 'install-local.sh supports Linux and macOS; use install-local.ps1 on Windows.' >&2
    exit 1
    ;;
esac
build_dir="${build_dir:-$default_build_dir}"
[[ -d "$build_dir" ]] || { echo "Build output not found at: $build_dir" >&2; exit 1; }

if [[ "$os" == darwin ]]; then
  app_src="$(find "$build_dir" -maxdepth 3 -type d -name '*.app' -print -quit)"
  [[ -n "$app_src" ]] || { echo "Expected an .app bundle inside: $build_dir" >&2; exit 1; }
  app_dir="${NUSASHELL_MAC_INSTALL_DIR:-$home_dir/Applications}"
  mkdir -p "$app_dir"
  rm -rf "$app_dir/NusaShell Desktop.app"
  mv "$app_src" "$app_dir/NusaShell Desktop.app"
  if command -v xattr >/dev/null 2>&1; then
    xattr -dr com.apple.quarantine "$app_dir/NusaShell Desktop.app" 2>/dev/null || true
  fi
  echo "Installed NusaShell Electron wrapper $version in $app_dir/NusaShell Desktop.app."
  exit 0
fi

[[ -x "$build_dir/nusashell-desktop" ]] || { echo "Expected executable $build_dir/nusashell-desktop" >&2; exit 1; }
root="${NUSASHELL_ELECTRON_INSTALL_ROOT:-$home_dir/.local/share/nusashell-electron}"
versions="$root/versions"
current="$root/current"
bin_dir="$home_dir/.local/bin"
mkdir -p "$versions" "$bin_dir" "$home_dir/.local/share/applications"
previous_version=''
if [[ -e "$current" || -L "$current" ]]; then
  previous_target="$(readlink "$current" 2>/dev/null || true)"
  [[ -z "$previous_target" ]] || previous_version="$(basename "$previous_target")"
fi

target="$versions/$version"
if [[ -e "$target" && ! -x "$target/nusashell-desktop" ]]; then rm -rf "$target"; fi
rm -rf "$target"
staging="$versions/.staging-${version}-$$"
rm -rf "$staging"
mkdir -p "$staging"
cp -R "$build_dir/." "$staging/"
[[ -x "$staging/nusashell-desktop" ]] || { rm -rf "$staging"; echo 'Local Electron package is missing nusashell-desktop.' >&2; exit 1; }
mv "$staging" "$target"

no_sandbox=0
sandbox="$target/chrome-sandbox"
sandbox_ok=0
userns_ok=0
if command -v unshare >/dev/null 2>&1 && unshare -Ur true >/dev/null 2>&1; then userns_ok=1; fi
if [[ -e "$sandbox" ]]; then
  mode="$(stat -c '%a' "$sandbox" 2>/dev/null || echo 0)"
  owner="$(stat -c '%u' "$sandbox" 2>/dev/null || echo 1)"
  if [[ "$owner" == 0 && "$mode" == 4755 ]]; then sandbox_ok=1; fi
fi
if [[ "$sandbox_ok" != 1 && "$userns_ok" == 1 ]]; then
  sandbox_ok=1
  [[ ! -e "$sandbox" ]] || mv -f "$sandbox" "$sandbox.disabled"
fi
if [[ "$sandbox_ok" != 1 ]]; then
  [[ ! -e "$sandbox" ]] || mv -f "$sandbox" "$sandbox.disabled"
  no_sandbox=1
  echo 'Chromium sandbox helper/user namespaces unavailable; launcher will use --no-sandbox.' >&2
fi

ln -sfn "$target" "$root/.current-$version"
mv -Tf "$root/.current-$version" "$current"
for candidate in "$versions"/*; do
  [[ -d "$candidate" && ! -L "$candidate" ]] || continue
  candidate_version="$(basename "$candidate")"
  [[ "$candidate_version" == "$version" || "$candidate_version" == "$previous_version" ]] && continue
  if command -v pgrep >/dev/null 2>&1 && pgrep -f "$candidate/nusashell-desktop" >/dev/null 2>&1; then
    echo "Keeping old version $candidate_version (process still running)." >&2
  else
    rm -rf "$candidate"
  fi
done
if [[ "$no_sandbox" == 1 ]]; then
  printf '#!/usr/bin/env sh\nexec "%s/nusashell-desktop" --no-sandbox "$@"\n' "$current" > "$bin_dir/nusashell-desktop"
else
  printf '#!/usr/bin/env sh\nexec "%s/nusashell-desktop" "$@"\n' "$current" > "$bin_dir/nusashell-desktop"
fi
chmod 0755 "$bin_dir/nusashell-desktop"
cat > "$home_dir/.local/share/applications/nusashell-desktop.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=NusaShell Desktop
Comment=NusaShell — local AI shell
Exec=$bin_dir/nusashell-desktop
Icon=$current/resources/nusashell.png
Terminal=false
Categories=Utility;Development;
EOF
echo "Installed NusaShell Electron wrapper $version. Run: nusashell-desktop"
