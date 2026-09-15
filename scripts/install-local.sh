#!/usr/bin/env bash
# Install NusaShell from this checkout: build the Go core, then optionally
# build+install the desktop pet (Linux) and Electron wrapper.
#
# This is the local counterpart of scripts/install.sh (which downloads GitHub
# releases). Full product install from a clone:
#   make install
#   # or: bash scripts/install-local.sh
#
# Electron-only (used by `make -C apps/electron install-local` after package):
#   bash scripts/install-local.sh --electron-only
set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
home_dir="${HOME:?HOME must be set}"
semver_re='^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$'

electron_only=0
electron_override="${NUSASHELL_INSTALL_ELECTRON:-}"
pets_override="${NUSASHELL_INSTALL_PETS:-}"
service_override="${NUSASHELL_INSTALL_SERVICE:-}"

usage() {
  cat <<'EOF'
Usage: install-local.sh [options]

Build and install NusaShell from this repository checkout (not GitHub releases).
The Go core is always built and installed. Optional components are prompted
when a terminal is available (default: no).

Options:
  --electron-only         Install only a prebuilt Electron package (no Go/pets).
  --install-electron      Build and install the Electron desktop wrapper.
  --no-electron           Do not install Electron.
  --install-service       Install the login service for the Go core (autostart).
  --no-service            Do not install the login service.
  --install-pets          Build and install the desktop pet (Linux only).
  --no-pets               Do not install the desktop pet.
  -h, --help              Show this help.

Environment:
  NUSASHELL_INSTALL_ELECTRON   1/yes or 0/no; overrides the prompt.
  NUSASHELL_INSTALL_SERVICE    1/yes or 0/no; overrides the prompt.
  NUSASHELL_INSTALL_PETS       1/yes or 0/no; overrides the prompt (Linux only).
  NUSASHELL_NON_INTERACTIVE    1 skips optional components by default.
  NUSASHELL_GO_INSTALL_ROOT    Override the Go core installation root.
  NUSASHELL_ELECTRON_INSTALL_ROOT
                               Override the Linux Electron installation root.
  NUSASHELL_PETS_INSTALL_ROOT  Override the Linux desktop pet installation root.
  NUSASHELL_MAC_INSTALL_DIR    Override the macOS application directory.
  NUSASHELL_BUILD_DIR          Prebuilt Electron package dir (--electron-only).
  NUSASHELL_LOCAL_GO_BINARY    Use this binary instead of building the Go core.
  NUSASHELL_LOCAL_PETS_DIR     Use this stage dir (nusashell-pets + assets/) instead of building pets.
EOF
}

while (($# > 0)); do
  case "$1" in
    --electron-only) electron_only=1; shift ;;
    --install-electron) electron_override=1; shift ;;
    --no-electron) electron_override=0; shift ;;
    --install-service) service_override=1; shift ;;
    --no-service) service_override=0; shift ;;
    --install-pets) pets_override=1; shift ;;
    --no-pets) pets_override=0; shift ;;
    --help|-h) usage; exit 0 ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

fail() {
  echo "NusaShell local installer: $*" >&2
  exit 1
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail 'install-local.sh supports Linux and macOS; use install-local.ps1 on Windows.' ;;
esac

validate_choice() {
  local value="$1" name="$2"
  case "${value,,}" in
    ''|1|yes|y|true|0|no|n|false) ;;
    *) fail "$name must be 1/yes or 0/no, got: $value" ;;
  esac
}

prompt_yes_no() {
  local override="$1" question="$2" answer=''
  validate_choice "$override" 'Installer choice'
  case "${override,,}" in
    1|yes|y|true) return 0 ;;
    0|no|n|false) return 1 ;;
  esac
  if [[ "${NUSASHELL_NON_INTERACTIVE:-}" == 1 ]]; then
    echo "$question skipped (NUSASHELL_NON_INTERACTIVE=1)." >&2
    return 1
  fi
  if [[ -r /dev/tty ]] && exec 3<>/dev/tty 2>/dev/null; then
    printf '%s [y/N] ' "$question" >&3
    IFS= read -r answer <&3 || answer=''
    exec 3>&-
  elif [[ -t 0 ]]; then
    printf '%s [y/N] ' "$question" >&2
    IFS= read -r answer || answer=''
  else
    echo "$question skipped (no interactive terminal)." >&2
    return 1
  fi
  case "${answer,,}" in
    y|yes) return 0 ;;
    *) return 1 ;;
  esac
}

read_previous_version() {
  local current="$1" previous_target
  previous_target="$(readlink "$current" 2>/dev/null || true)"
  [[ -n "$previous_target" ]] || return 0
  basename "$previous_target"
}

activate_unix_version() {
  local root="$1" target="$2" version="$3" current
  current="$root/current"
  ln -sfn "$target" "$root/.current-${version}"
  if [[ "$os" == linux ]]; then
    mv -Tf "$root/.current-${version}" "$current"
  else
    [[ ! -d "$current" || -L "$current" ]] || fail "Refusing to replace a non-symlink: $current"
    rm -f "$current"
    mv "$root/.current-${version}" "$current"
  fi
}

prune_unix_versions() {
  local versions="$1" active="$2" previous="$3" binary_name="$4" candidate candidate_version
  shopt -s nullglob
  for candidate in "$versions"/*; do
    [[ -d "$candidate" && ! -L "$candidate" ]] || continue
    candidate_version="$(basename "$candidate")"
    [[ "$candidate_version" == "$active" || "$candidate_version" == "$previous" ]] && continue
    if command -v pgrep >/dev/null 2>&1 && pgrep -f "$candidate/$binary_name" >/dev/null 2>&1; then
      echo "Keeping old version $candidate_version (process still running)." >&2
    else
      rm -rf "$candidate"
    fi
  done
  shopt -u nullglob
}

install_electron_from_build() {
  local version build_dir root versions current previous target staging no_sandbox sandbox userns_ok sandbox_ok app_src app_dir bin_dir
  version="$(tr -d '[:space:]' < "$repo_root/apps/electron/VERSION")"
  [[ "$version" =~ $semver_re ]] || fail "Invalid apps/electron/VERSION: $version"

  if [[ "$os" == darwin ]]; then
    if [[ -d "$repo_root/apps/electron/dist/mac" ]]; then
      build_dir="${NUSASHELL_BUILD_DIR:-$repo_root/apps/electron/dist/mac}"
    elif [[ -d "$repo_root/apps/electron/dist/mac-arm64" ]]; then
      build_dir="${NUSASHELL_BUILD_DIR:-$repo_root/apps/electron/dist/mac-arm64}"
    else
      build_dir="${NUSASHELL_BUILD_DIR:-$repo_root/apps/electron/dist/mac-x64}"
    fi
  else
    build_dir="${NUSASHELL_BUILD_DIR:-$repo_root/apps/electron/dist/linux-unpacked}"
  fi
  [[ -d "$build_dir" ]] || fail "Electron build output not found at: $build_dir (run package first)"

  if [[ "$os" == darwin ]]; then
    app_src="$(find "$build_dir" -maxdepth 3 -type d -name '*.app' -print -quit)"
    [[ -n "$app_src" ]] || fail "Expected an .app bundle inside: $build_dir"
    app_dir="${NUSASHELL_MAC_INSTALL_DIR:-$home_dir/Applications}"
    mkdir -p "$app_dir"
    rm -rf "$app_dir/NusaShell Desktop.app"
    mv "$app_src" "$app_dir/NusaShell Desktop.app"
    if command -v xattr >/dev/null 2>&1; then
      xattr -dr com.apple.quarantine "$app_dir/NusaShell Desktop.app" 2>/dev/null || true
    fi
    echo "Installed NusaShell Electron wrapper $version in $app_dir/NusaShell Desktop.app."
    return 0
  fi

  [[ -x "$build_dir/nusashell-desktop" ]] || fail "Expected executable $build_dir/nusashell-desktop"
  root="${NUSASHELL_ELECTRON_INSTALL_ROOT:-$home_dir/.local/share/nusashell-electron}"
  versions="$root/versions"
  current="$root/current"
  bin_dir="$home_dir/.local/bin"
  mkdir -p "$versions" "$bin_dir" "$home_dir/.local/share/applications"
  previous="$(read_previous_version "$current")"

  target="$versions/$version"
  if [[ -e "$target" && ! -x "$target/nusashell-desktop" ]]; then rm -rf "$target"; fi
  rm -rf "$target"
  staging="$versions/.staging-${version}-$$"
  rm -rf "$staging"
  mkdir -p "$staging"
  cp -R "$build_dir/." "$staging/"
  [[ -x "$staging/nusashell-desktop" ]] || { rm -rf "$staging"; fail 'Local Electron package is missing nusashell-desktop.'; }
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

  activate_unix_version "$root" "$target" "$version"
  prune_unix_versions "$versions" "$version" "$previous" nusashell-desktop
  if [[ "$no_sandbox" == 1 ]]; then
    printf '#!/usr/bin/env sh\nexec "%s/nusashell-desktop" --no-sandbox "$@"\n' "$current" > "$bin_dir/nusashell-desktop"
  else
    printf '#!/usr/bin/env sh\nexec "%s/nusashell-desktop" "$@"\n' "$current" > "$bin_dir/nusashell-desktop"
  fi
  chmod 0755 "$bin_dir/nusashell-desktop"
  cat > "$home_dir/.local/share/applications/nusashell-desktop.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=NusaShell
Comment=NusaShell
Exec=$bin_dir/nusashell-desktop
Icon=$current/resources/nusashell.png
Terminal=false
Categories=Utility;Development;
EOF
  echo "Installed NusaShell Electron wrapper $version. Run: nusashell-desktop"
}

if [[ "$electron_only" == 1 ]]; then
  install_electron_from_build
  exit 0
fi

install_service=0
install_electron=0
install_pets=0
go_current=''

if prompt_yes_no "$service_override" 'Install nusashell as a login service (autostart)?'; then
  install_service=1
fi
if prompt_yes_no "$electron_override" 'Build and install Electron desktop wrapper?'; then
  install_electron=1
fi
if [[ "$os" == linux ]]; then
  if prompt_yes_no "$pets_override" 'Build and install desktop pet (Linux only)?'; then
    install_pets=1
  fi
else
  if [[ -n "$pets_override" ]]; then
    echo 'Desktop pet is Linux-only; ignoring pets options on macOS.' >&2
  fi
fi

install_go_from_checkout() {
  local version root versions current previous target staging binary
  version="$(tr -d '[:space:]' < "$repo_root/VERSION")"
  [[ "$version" =~ $semver_re ]] || fail "Invalid VERSION: $version"

  root="${NUSASHELL_GO_INSTALL_ROOT:-$home_dir/.local/share/nusashell}"
  versions="$root/versions"
  current="$root/current"
  mkdir -p "$versions" "$home_dir/.local/bin"
  previous="$(read_previous_version "$current")"
  target="$versions/$version"
  staging="$versions/.staging-${version}-$$"
  rm -rf "$staging"
  mkdir -p "$staging"

  if [[ -n "${NUSASHELL_LOCAL_GO_BINARY:-}" ]]; then
    binary="${NUSASHELL_LOCAL_GO_BINARY}"
    [[ -x "$binary" ]] || fail "NUSASHELL_LOCAL_GO_BINARY is not executable: $binary"
    cp "$binary" "$staging/nusashell"
    chmod 0755 "$staging/nusashell"
  else
    command -v go >/dev/null 2>&1 || fail 'Go is required to build the NusaShell core from this checkout.'
    echo "Building Go core $version…"
    (
      cd "$repo_root"
      go build -buildvcs=false -ldflags "-X main.version=$version" -o "$staging/nusashell" ./cmd/nusashell
    ) || fail 'Go core build failed.'
  fi
  [[ -x "$staging/nusashell" ]] || fail 'Built nusashell is missing or not executable.'

  rm -rf "$target"
  mv "$staging" "$target"
  activate_unix_version "$root" "$target" "$version"
  prune_unix_versions "$versions" "$version" "$previous" nusashell
  printf '#!/usr/bin/env sh\nexec "%s/nusashell" "$@"\n' "$current" > "$home_dir/.local/bin/nusashell"
  chmod 0755 "$home_dir/.local/bin/nusashell"
  go_current="$current"
  echo "Installed NusaShell Go core $version from checkout. Run: nusashell"
}

install_pets_from_checkout() {
  local version root versions current previous target staging pets_src
  [[ "$os" == linux ]] || fail 'Desktop pet install is Linux-only.'
  version="$(tr -d '[:space:]' < "$repo_root/apps/pets/VERSION")"
  [[ "$version" =~ $semver_re ]] || fail "Invalid apps/pets/VERSION: $version"

  root="${NUSASHELL_PETS_INSTALL_ROOT:-$home_dir/.local/share/nusashell-pets}"
  versions="$root/versions"
  current="$root/current"
  mkdir -p "$versions" "$home_dir/.local/bin"
  previous="$(read_previous_version "$current")"
  target="$versions/$version"
  staging="$versions/.staging-${version}-$$"
  rm -rf "$staging"
  mkdir -p "$staging"

  if [[ -n "${NUSASHELL_LOCAL_PETS_DIR:-}" ]]; then
    pets_src="${NUSASHELL_LOCAL_PETS_DIR}"
    [[ -x "$pets_src/nusashell-pets" ]] || fail "NUSASHELL_LOCAL_PETS_DIR missing nusashell-pets: $pets_src"
    [[ -f "$pets_src/assets/pets/config.json" ]] || fail "NUSASHELL_LOCAL_PETS_DIR missing assets/pets: $pets_src"
    cp -R "$pets_src/." "$staging/"
  else
    command -v go >/dev/null 2>&1 || fail 'Go is required to build the desktop pet.'
    echo "Building desktop pet $version…"
    (
      cd "$repo_root/apps/pets"
      go build -buildvcs=false -tags sdl2 -o "$staging/nusashell-pets" ./cmd/pets
      cp -R assets "$staging/"
    ) || fail 'Desktop pet build failed (SDL2 tags/libraries required on Linux).'
  fi
  [[ -x "$staging/nusashell-pets" ]] || fail 'Built nusashell-pets is missing or not executable.'
  [[ -f "$staging/assets/pets/config.json" ]] || fail 'Pets assets/pets/config.json is missing.'

  rm -rf "$target"
  mv "$staging" "$target"
  activate_unix_version "$root" "$target" "$version"
  prune_unix_versions "$versions" "$version" "$previous" nusashell-pets
  printf '#!/usr/bin/env sh\nexec "%s/nusashell-pets" --assets "%s/assets/pets" "$@"\n' "$current" "$current" > "$home_dir/.local/bin/nusashell-pets"
  chmod 0755 "$home_dir/.local/bin/nusashell-pets"
  echo "Installed NusaShell desktop pet $version from checkout. Run: nusashell-pets"
}

build_and_install_electron() {
  echo 'Packaging Electron desktop wrapper…'
  make -C "$repo_root/apps/electron" package || fail 'Electron package failed.'
  install_electron_from_build
}

install_go_from_checkout

if [[ "$install_service" == 1 ]]; then
  if [[ -z "$go_current" || ! -x "$go_current/nusashell" ]]; then
    fail 'Service install requires the NusaShell Go core.'
  fi
  if ! "$go_current/nusashell" service install; then
    echo 'NusaShell service install failed; run "nusashell service install" manually for details.' >&2
  else
    echo 'NusaShell starts automatically at login. Manage it with: nusashell service status'
  fi
fi

if [[ "$install_pets" == 1 ]]; then
  install_pets_from_checkout
fi

if [[ "$install_electron" == 1 ]]; then
  build_and_install_electron
fi
