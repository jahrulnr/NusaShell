#!/usr/bin/env bash
# Install the NusaShell Go core and, optionally, its Electron desktop wrapper
# and first-party MCP plugins. The script is intentionally self-contained so
# it remains safe to use through `curl ... | bash`.
set -Eeuo pipefail

usage() {
  cat <<'EOF'
Usage: install.sh [options]

The Go core is always installed. Electron and NusaShell-mcp are opt-in:
the installer asks about both when an interactive terminal is available.

Options:
  --version VERSION       Pin a release version (otherwise latest).
  --electron-version VERSION
                           Pin the optional Electron version (otherwise latest Electron).
  --install-electron      Install the Electron desktop wrapper.
  --no-electron            Do not install Electron.
  --install-mcp           Install first-party NusaShell-mcp plugins.
  --no-mcp                Do not install NusaShell-mcp plugins.
  -h, --help              Show this help.

Environment:
  NUSASHELL_VERSION             Pin a release version (otherwise latest).
  NUSASHELL_ELECTRON_VERSION    Pin the optional Electron version (otherwise latest Electron).
  NUSASHELL_INSTALL_ELECTRON   1/yes or 0/no; overrides the prompt.
  NUSASHELL_INSTALL_MCP        1/yes or 0/no; overrides the prompt.
  NUSASHELL_NON_INTERACTIVE    1 skips optional components by default.
  NUSASHELL_REPOSITORY         GitHub repository (default: jahrulnr/NusaShell).
  NUSASHELL_RELEASE_BASE       Release base URL (for mirrors/tests).
  NUSASHELL_RELEASE_INDEX      Stream index URL (for mirrors/tests).
  NUSASHELL_GO_INSTALL_ROOT    Override the Go core installation root.
  NUSASHELL_ELECTRON_INSTALL_ROOT
                                Override the Linux Electron installation root.
  NUSASHELL_MAC_INSTALL_DIR    Override the macOS application directory.
  NUSASHELL_MCP_REPOSITORY     MCP repository slug (default: jahrulnr/NusaShell-mcp).
  NUSASHELL_MCP_PLUGINS        Space/comma-separated plugin keys to install.
EOF
}

requested_version="${NUSASHELL_VERSION:-}"
requested_electron_version="${NUSASHELL_ELECTRON_VERSION:-}"
electron_override="${NUSASHELL_INSTALL_ELECTRON:-}"
mcp_override="${NUSASHELL_INSTALL_MCP:-}"
while (($# > 0)); do
  case "$1" in
    --version)
      (($# >= 2)) || { echo '--version requires a value' >&2; exit 2; }
      requested_version="$2"
      shift 2
      ;;
    --version=*)
      requested_version="${1#*=}"
      shift
      ;;
    --electron-version)
      (($# >= 2)) || { echo '--electron-version requires a value' >&2; exit 2; }
      requested_electron_version="$2"
      shift 2
      ;;
    --electron-version=*)
      requested_electron_version="${1#*=}"
      shift
      ;;
    --install-electron)
      electron_override=1
      shift
      ;;
    --no-electron)
      electron_override=0
      shift
      ;;
    --install-mcp)
      mcp_override=1
      shift
      ;;
    --no-mcp)
      mcp_override=0
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

repo="${NUSASHELL_REPOSITORY:-jahrulnr/NusaShell}"
release_base="${NUSASHELL_RELEASE_BASE:-https://github.com/${repo}/releases}"
release_base="${release_base%/}"
release_index_url="${NUSASHELL_RELEASE_INDEX:-https://raw.githubusercontent.com/${repo}/master/release-versions.json}"
home_dir="${HOME:?HOME must be set}"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/nusashell-install.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

fail() {
  echo "NusaShell installer: $*" >&2
  exit 1
}

download() {
  local url="$1" destination="$2"
  case "$url" in
    https://*) ;;
    *) fail "Refusing non-HTTPS download URL: $url" ;;
  esac
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error --proto '=https' --tlsv1.2 "$url" -o "$destination"
  elif command -v wget >/dev/null 2>&1; then
    wget --https-only -qO "$destination" "$url"
  else
    fail 'curl or wget is required.'
  fi
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail 'Linux and macOS are supported by install.sh; use install.ps1 on Windows.' ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=x64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "Unsupported CPU architecture: $(uname -m)" ;;
esac

semver_re='^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$'
if [[ -n "$requested_version" && ! "$requested_version" =~ $semver_re ]]; then
  fail "Invalid release version: $requested_version"
fi
if [[ -n "$requested_electron_version" && ! "$requested_electron_version" =~ $semver_re ]]; then
  fail "Invalid Electron release version: $requested_electron_version"
fi

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

if prompt_yes_no "$electron_override" 'Install Electron desktop wrapper?'; then
  install_electron=1
else
  install_electron=0
fi
if prompt_yes_no "$mcp_override" 'Install MCP plugins from NusaShell-mcp?'; then
  install_mcp=1
else
  install_mcp=0
fi

release_index=''
release_version=''
release_tag=''
release_manifest=''

download_release_index() {
  if [[ -z "$release_index" ]]; then
    release_index="$tmp_dir/release-versions.json"
    download "$release_index_url" "$release_index" || fail 'No NusaShell release stream index is available.'
  fi
}

release_index_value() {
  local stream="$1" field="$2" block
  block="$(sed -n "/^[[:space:]]*\"${stream}\"[[:space:]]*:[[:space:]]*{/,/^[[:space:]]*}/p" "$release_index")"
  printf '%s\n' "$block" | sed -nE "s/^[[:space:]]*\"${field}\"[[:space:]]*:[[:space:]]*\"([^\"]+)\"[[:space:]]*,?[[:space:]]*$/\1/p" | head -n 1
}

resolve_release_stream() {
  local stream="$1" requested="$2" expected_manifest
  expected_manifest='latest.json'
  [[ "$stream" == electron ]] && expected_manifest='electron-latest.json'
  if [[ -n "$requested" ]]; then
    release_version="$requested"
    release_tag="${stream}-v${requested}"
    release_manifest="$expected_manifest"
  else
    download_release_index
    release_version="$(release_index_value "$stream" version)"
    release_tag="$(release_index_value "$stream" tag)"
    release_manifest="$(release_index_value "$stream" manifest)"
    [[ -n "$release_version" && -n "$release_tag" ]] || fail "No published NusaShell ${stream} release is available."
    [[ -n "$release_manifest" ]] || release_manifest="$expected_manifest"
  fi

  [[ "$release_version" =~ $semver_re ]] || fail "${stream} release index contains an invalid version."
  [[ "$release_tag" == "${stream}-v${release_version}" && "$release_tag" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*$ ]] || fail "${stream} release index contains an unsafe tag."
  [[ "$release_manifest" == "$expected_manifest" ]] || fail "${stream} release index contains an invalid manifest name."
}

# The generated release manifest is deliberately line-oriented so the piped
# installer needs no Node.js, Python, jq, or npm.
manifest_value() {
  local manifest="$1" key="$2" field="${3:-}" block
  if [[ "$key" == version ]]; then
    sed -nE 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"([^"]+)"[[:space:]]*,?[[:space:]]*$/\1/p' "$manifest" | head -n 1
    return
  fi
  block="$(sed -n "/^[[:space:]]*\"${key}\"[[:space:]]*:[[:space:]]*{/,/^[[:space:]]*}/p" "$manifest")"
  printf '%s\n' "$block" | sed -nE "s/^[[:space:]]*\"${field}\"[[:space:]]*:[[:space:]]*\"([^\"]+)\"[[:space:]]*,?[[:space:]]*$/\1/p" | head -n 1
}

verify_payload() {
  local payload="$1" expected="$2" actual
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || fail 'Release manifest contains an invalid SHA-256 digest.'
  if [[ "$os" == linux ]]; then
    command -v sha256sum >/dev/null 2>&1 || fail 'sha256sum is required on Linux.'
    actual="$(sha256sum "$payload" | awk '{print $1}')"
  else
    command -v shasum >/dev/null 2>&1 || fail 'shasum is required on macOS.'
    actual="$(shasum -a 256 "$payload" | awk '{print $1}')"
  fi
  [[ "$actual" == "${expected,,}" ]] || fail 'Checksum verification failed; refusing to install.'
}

assert_safe_archive_name() {
  local name="$1"
  [[ "$name" == "$(basename "$name")" && "$name" != *\* ]] || fail 'Release payload name is unsafe.'
}

safe_extract_tar() {
  local archive="$1" destination="$2" list entry
  command -v tar >/dev/null 2>&1 || fail 'tar is required to install this release.'
  list="$tmp_dir/archive-$(basename "$archive").list"
  tar -tzf "$archive" > "$list" || fail 'Could not inspect the release archive.'
  while IFS= read -r entry; do
    case "$entry" in
      /*|../*|*/../*|*/..|*\*) fail "Release archive entry is unsafe: $entry" ;;
    esac
  done < "$list"
  mkdir -p "$destination"
  tar -xzf "$archive" -C "$destination"
}

read_previous_version() {
  local current="$1" target=''
  if [[ -e "$current" || -L "$current" ]]; then
    target="$(readlink "$current" 2>/dev/null || true)"
    [[ -z "$target" ]] || basename "$target"
  fi
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
  local versions="$1" active="$2" previous="$3" candidate candidate_version
  shopt -s nullglob
  for candidate in "$versions"/*; do
    [[ -d "$candidate" && ! -L "$candidate" ]] || continue
    candidate_version="$(basename "$candidate")"
    [[ "$candidate_version" == "$active" || "$candidate_version" == "$previous" ]] && continue
    if command -v pgrep >/dev/null 2>&1 && pgrep -f "$candidate/nusashell" >/dev/null 2>&1; then
      echo "Keeping old version $candidate_version (process still running)." >&2
    else
      rm -rf "$candidate"
    fi
  done
  shopt -u nullglob
}

install_core_unix() {
  local manifest="$tmp_dir/latest.json" resolved_version file_name expected_sha payload root versions current previous target staging
  resolve_release_stream go "$requested_version"
  download "${release_base}/download/${release_tag}/${release_manifest}" "$manifest" || fail 'No published NusaShell Go release is available for this platform.'
  resolved_version="$(manifest_value "$manifest" version)"
  [[ "$resolved_version" =~ $semver_re ]] || fail 'Release manifest contains an invalid version.'
  [[ "$resolved_version" == "$release_version" ]] || fail "Go release manifest version $resolved_version does not match release index $release_version."
  file_name="$(manifest_value "$manifest" "${os}-${arch}" name)"
  expected_sha="$(manifest_value "$manifest" "${os}-${arch}" sha256)"
  [[ -n "$file_name" && -n "$expected_sha" ]] || fail "No Go release payload is published for ${os}-${arch}."
  assert_safe_archive_name "$file_name"
  payload="$tmp_dir/$file_name"
  download "${release_base}/download/${release_tag}/${file_name}" "$payload" || fail 'Could not download the NusaShell Go payload.'
  verify_payload "$payload" "$expected_sha"

  root="${NUSASHELL_GO_INSTALL_ROOT:-$home_dir/.local/share/nusashell}"
  versions="$root/versions"
  current="$root/current"
  mkdir -p "$versions"
  previous="$(read_previous_version "$current")"
  target="$versions/$resolved_version"
  if [[ ! -x "$target/nusashell" ]]; then
    [[ ! -e "$target" ]] || rm -rf "$target"
    staging="$versions/.staging-${resolved_version}-$$"
    rm -rf "$staging"
    safe_extract_tar "$payload" "$staging"
    [[ -x "$staging/nusashell" ]] || fail 'The Go release did not contain the nusashell executable at its root.'
    mv "$staging" "$target"
  fi
  [[ -x "$target/nusashell" ]] || fail 'The Go release did not contain the nusashell executable.'
  activate_unix_version "$root" "$target" "$resolved_version"
  prune_unix_versions "$versions" "$resolved_version" "$previous"

  mkdir -p "$home_dir/.local/bin"
  printf '#!/usr/bin/env sh\nexec "%s/nusashell" "$@"\n' "$current" > "$home_dir/.local/bin/nusashell"
  chmod 0755 "$home_dir/.local/bin/nusashell"
  echo "Installed NusaShell Go core $resolved_version. Run: nusashell"
}

install_electron_unix() {
  local manifest="$tmp_dir/electron-latest.json" electron_version file_name expected_sha payload root versions current previous target staging no_sandbox sandbox userns_ok sandbox_ok unpacked app_src app_dir
  resolve_release_stream electron "$requested_electron_version"
  download "${release_base}/download/${release_tag}/${release_manifest}" "$manifest" || fail 'Electron was selected, but no Electron release manifest is available.'
  electron_version="$(manifest_value "$manifest" version)"
  [[ "$electron_version" == "$release_version" ]] || fail "Electron release manifest version $electron_version does not match release index $release_version."
  file_name="$(manifest_value "$manifest" "${os}-${arch}" name)"
  expected_sha="$(manifest_value "$manifest" "${os}-${arch}" sha256)"
  [[ -n "$file_name" && -n "$expected_sha" ]] || fail "No Electron release payload is published for ${os}-${arch}."
  assert_safe_archive_name "$file_name"
  payload="$tmp_dir/$file_name"
  download "${release_base}/download/${release_tag}/${file_name}" "$payload" || fail 'Could not download the Electron payload.'
  verify_payload "$payload" "$expected_sha"

  if [[ "$os" == darwin ]]; then
    command -v unzip >/dev/null 2>&1 || fail 'unzip is required on macOS.'
    unpacked="$tmp_dir/electron-unpacked"
    mkdir -p "$unpacked"
    unzip -q "$payload" -d "$unpacked"
    app_src="$(find "$unpacked" -type d -name '*.app' -print -quit)"
    [[ -n "$app_src" ]] || fail 'The macOS Electron release did not contain an application bundle.'
    app_dir="${NUSASHELL_MAC_INSTALL_DIR:-$home_dir/Applications}"
    mkdir -p "$app_dir"
    rm -rf "$app_dir/NusaShell Desktop.app"
    mv "$app_src" "$app_dir/NusaShell Desktop.app"
    if command -v xattr >/dev/null 2>&1; then
      xattr -dr com.apple.quarantine "$app_dir/NusaShell Desktop.app" 2>/dev/null || true
    fi
    echo "Installed NusaShell Electron wrapper $electron_version in $app_dir/NusaShell Desktop.app."
    return
  fi

  root="${NUSASHELL_ELECTRON_INSTALL_ROOT:-$home_dir/.local/share/nusashell-electron}"
  versions="$root/versions"
  current="$root/current"
  mkdir -p "$versions"
  previous="$(read_previous_version "$current")"
  target="$versions/$electron_version"
  if [[ ! -x "$target/nusashell-desktop" ]]; then
    [[ ! -e "$target" ]] || rm -rf "$target"
    staging="$versions/.staging-${electron_version}-$$"
    rm -rf "$staging"
    safe_extract_tar "$payload" "$staging"
    [[ -x "$staging/nusashell-desktop" ]] || fail 'The Electron release did not contain nusashell-desktop at its root.'
    mv "$staging" "$target"
  fi
  [[ -x "$target/nusashell-desktop" ]] || fail 'The Electron release did not contain the nusashell-desktop executable.'

  no_sandbox=0
  if [[ "$os" == linux ]]; then
    sandbox="$target/chrome-sandbox"
    sandbox_ok=0
    userns_ok=0
    if command -v unshare >/dev/null 2>&1 && unshare -Ur true >/dev/null 2>&1; then userns_ok=1; fi
    if [[ -e "$sandbox" ]]; then
      local mode owner
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
      echo 'Chromium sandbox helper/user namespaces unavailable; Electron launcher will use --no-sandbox.' >&2
    fi
  fi

  activate_unix_version "$root" "$target" "$electron_version"
  prune_unix_versions "$versions" "$electron_version" "$previous"
  mkdir -p "$home_dir/.local/bin" "$home_dir/.local/share/applications"
  if [[ "$no_sandbox" == 1 ]]; then
    printf '#!/usr/bin/env sh\nexec "%s/nusashell-desktop" --no-sandbox "$@"\n' "$current" > "$home_dir/.local/bin/nusashell-desktop"
  else
    printf '#!/usr/bin/env sh\nexec "%s/nusashell-desktop" "$@"\n' "$current" > "$home_dir/.local/bin/nusashell-desktop"
  fi
  chmod 0755 "$home_dir/.local/bin/nusashell-desktop"
  cat > "$home_dir/.local/share/applications/nusashell-desktop.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=NusaShell Desktop
Comment=NusaShell — local AI shell
Exec=$home_dir/.local/bin/nusashell-desktop
Icon=$current/resources/nusashell.png
Terminal=false
Categories=Utility;Development;
EOF
  echo "Installed NusaShell Electron wrapper $electron_version. Run: nusashell-desktop"
}

mcp_data_dir() {
  if [[ -n "${NUSASHELL_DATA_DIR:-}" ]]; then
    printf '%s\n' "$NUSASHELL_DATA_DIR"
  elif [[ "$os" == linux ]]; then
    printf '%s/nusashell\n' "${XDG_CONFIG_HOME:-$home_dir/.config}"
  else
    printf '%s/nusashell\n' "$home_dir/Library/Application Support"
  fi
}

mcp_catalog_value() {
  local manifest="$1" plugin="$2" field="$3" block
  block="$(sed -n "/^[[:space:]]*\"${plugin}\"[[:space:]]*:[[:space:]]*{/,/^[[:space:]]*}/p" "$manifest")"
  printf '%s\n' "$block" | sed -nE "s/^[[:space:]]*\"${field}\"[[:space:]]*:[[:space:]]*\"([^\"]+)\"[[:space:]]*,?[[:space:]]*$/\1/p" | head -n 1
}

extract_plugin_source() {
  local root="$1" plugin="$2" manifest_path
  manifest_path="$(find "$root" -type f -path "*/${plugin}/manifest.json" -print -quit 2>/dev/null || true)"
  if [[ -z "$manifest_path" ]]; then
    manifest_path="$(find "$root" -type f -name manifest.json -print -quit 2>/dev/null || true)"
  fi
  [[ -n "$manifest_path" ]] || fail "NusaShell-mcp archive has no manifest for $plugin."
  dirname "$manifest_path"
}

install_mcp_unix() {
  local mcp_repo="${NUSASHELL_MCP_REPOSITORY:-${NUSASHELL_MCP_REPO:-jahrulnr/NusaShell-mcp}}"
  local raw_base="${NUSASHELL_MCP_RAW_BASE:-https://raw.githubusercontent.com/${mcp_repo}/master}"
  local release_base_mcp="${NUSASHELL_MCP_RELEASE_BASE:-https://github.com/${mcp_repo}/releases/download}"
  local archive_base="${NUSASHELL_MCP_ARCHIVE_BASE:-https://github.com/${mcp_repo}/archive/refs/tags}"
  local catalog="$tmp_dir/mcp-versions.json" plugin_keys plugin version tag asset archive stage source plugin_id data_dir destination
  download "${raw_base}/versions.json" "$catalog" || fail 'Could not download NusaShell-mcp versions.json.'
  plugin_keys="${NUSASHELL_MCP_PLUGINS:-kanban notes whatsapp telegram}"
  plugin_keys="${plugin_keys//,/ }"
  data_dir="$(mcp_data_dir)"
  destination="$data_dir/plugins"
  mkdir -p "$destination"

  for plugin in $plugin_keys; do
    version="$(mcp_catalog_value "$catalog" "$plugin" version)"
    tag="$(mcp_catalog_value "$catalog" "$plugin" tag)"
    [[ -n "$version" && -n "$tag" ]] || fail "NusaShell-mcp catalog has no usable entry for $plugin."
    stage="$tmp_dir/mcp-${plugin}"
    rm -rf "$stage"
    mkdir -p "$stage"
    source=''

    # Existing NusaShell-mcp releases contain Linux binaries. Use them on
    # Linux, and build from the tagged source on macOS/Windows-compatible
    # Unix environments so the installed stdio server matches the host.
    if [[ "$os" == linux ]]; then
      asset="${plugin}-${version}.tar.gz"
      archive="$tmp_dir/$asset"
      if download "${release_base_mcp}/${tag}/${asset}" "$archive"; then
        safe_extract_tar "$archive" "$stage/release"
        source="$(extract_plugin_source "$stage/release" "$plugin")"
      fi
    fi
    if [[ -z "$source" ]]; then
      command -v go >/dev/null 2>&1 || fail "MCP plugin $plugin has no native release for $os; install Go or use a NusaShell-mcp release with native assets."
      archive="$tmp_dir/mcp-${plugin}-source.tar.gz"
      download "${archive_base}/${tag}.tar.gz" "$archive" || fail "Could not download NusaShell-mcp source for $plugin."
      safe_extract_tar "$archive" "$stage/source"
      source="$(extract_plugin_source "$stage/source" "$plugin")"
      [[ -d "$source/mcp" ]] || fail "NusaShell-mcp source for $plugin has no mcp directory."
      (cd "$source/mcp" && go build -buildvcs=false -o server .) || fail "Could not build MCP plugin $plugin for $os."
    fi

    [[ -f "$source/manifest.json" && -x "$source/mcp/server" ]] || fail "MCP plugin $plugin is missing manifest.json or executable mcp/server."
    plugin_id="$(sed -nE 's/^[[:space:]]*"id"[[:space:]]*:[[:space:]]*"([A-Za-z0-9._-]+)"[[:space:]]*,?[[:space:]]*$/\1/p' "$source/manifest.json" | head -n 1)"
    [[ -n "$plugin_id" ]] || fail "MCP plugin $plugin has an invalid manifest id."
    rm -rf "${destination:?}/$plugin_id"
    mkdir -p "$destination/$plugin_id"
    cp -R "$source/." "$destination/$plugin_id/"
    echo "Installed MCP plugin: $plugin_id $version"
  done
  echo "NusaShell-mcp plugins are installed under $destination."
}

install_core_unix
if [[ "$install_electron" == 1 ]]; then
  install_electron_unix
fi
if [[ "$install_mcp" == 1 ]]; then
  install_mcp_unix
fi

if [[ ":$PATH:" != *":$home_dir/.local/bin:"* ]]; then
  echo "Add this to your shell profile: export PATH=\"\$HOME/.local/bin:\$PATH\"" >&2
fi
