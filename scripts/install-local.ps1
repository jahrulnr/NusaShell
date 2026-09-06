# Install NusaShell from this checkout: build the Go core, then optionally
# build+install the Electron desktop wrapper.
#
# Local counterpart of scripts/install.ps1 (which downloads GitHub releases).
# Full product install from a clone: make install / .\scripts\install-local.ps1
# Electron-only (after package): .\scripts\install-local.ps1 -ElectronOnly
[CmdletBinding()]
param(
  [switch]$ElectronOnly,
  [switch]$InstallElectron,
  [switch]$NoElectron,
  [switch]$InstallService,
  [switch]$NoService
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$semverPattern = '^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$'

$electronOverride = if ($InstallElectron) { '1' } elseif ($NoElectron) { '0' } elseif ($env:NUSASHELL_INSTALL_ELECTRON) { $env:NUSASHELL_INSTALL_ELECTRON } else { '' }
$serviceOverride = if ($InstallService) { '1' } elseif ($NoService) { '0' } elseif ($env:NUSASHELL_INSTALL_SERVICE) { $env:NUSASHELL_INSTALL_SERVICE } else { '' }

function Test-Choice([string]$Value, [string]$Name) {
  if ($Value -and $Value.ToLowerInvariant() -notin @('1', 'yes', 'y', 'true', '0', 'no', 'n', 'false')) {
    throw "$Name must be 1/yes or 0/no, got: $Value"
  }
}

function Get-OptionalChoice([string]$Override, [string]$Question, [string]$Name) {
  Test-Choice $Override $Name
  if ($Override) { return $Override.ToLowerInvariant() -in @('1', 'yes', 'y', 'true') }
  if ($env:NUSASHELL_NON_INTERACTIVE -eq '1') {
    Write-Host "$Question skipped (NUSASHELL_NON_INTERACTIVE=1)."
    return $false
  }
  $answer = Read-Host "$Question [y/N]"
  return $answer.ToLowerInvariant() -in @('y', 'yes')
}

function Get-PreviousVersion([string]$Current) {
  if (-not (Test-Path -LiteralPath $Current)) { return '' }
  try {
    return Split-Path ((Resolve-Path -LiteralPath $Current).Path.TrimEnd('\')) -Leaf
  } catch {
    return ''
  }
}

function Set-CurrentJunction([string]$Current, [string]$Target) {
  if (Test-Path -LiteralPath $Current) {
    $item = Get-Item -LiteralPath $Current -Force
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
      [IO.Directory]::Delete($Current)
    } else {
      Remove-Item -LiteralPath $Current -Recurse -Force
    }
  }
  New-Item -ItemType Junction -Path $Current -Target $Target | Out-Null
}

function Remove-OldVersions([string]$Versions, [string]$Active, [string]$Previous) {
  Get-ChildItem -LiteralPath $Versions -Directory |
    Where-Object { $_.Name -notin @($Active, $Previous) -and $_.Name -notlike '.staging-*' } |
    ForEach-Object {
      try { Remove-Item -LiteralPath $_.FullName -Recurse -Force }
      catch { Write-Warning "Keeping old version $($_.Name): $($_.Exception.Message)" }
    }
}

function Install-ElectronFromBuild {
  $version = (Get-Content -LiteralPath (Join-Path $repoRoot 'apps\electron\VERSION') -Raw).Trim()
  if ($version -notmatch $semverPattern) { throw "Invalid apps/electron/VERSION: $version" }

  $buildDir = if ($env:NUSASHELL_BUILD_DIR) {
    [IO.Path]::GetFullPath($env:NUSASHELL_BUILD_DIR)
  } else {
    Join-Path $repoRoot 'apps\electron\dist\win-unpacked'
  }
  if (-not (Test-Path -LiteralPath $buildDir -PathType Container)) {
    throw "Electron build output not found at $buildDir. Run make -C apps/electron package first or set NUSASHELL_BUILD_DIR."
  }
  if (-not (Test-Path -LiteralPath (Join-Path $buildDir 'nusashell-desktop.exe') -PathType Leaf)) {
    throw "Expected nusashell-desktop.exe inside $buildDir."
  }

  $root = if ($env:NUSASHELL_WINDOWS_ELECTRON_INSTALL_ROOT) {
    [IO.Path]::GetFullPath($env:NUSASHELL_WINDOWS_ELECTRON_INSTALL_ROOT)
  } else {
    Join-Path $env:LOCALAPPDATA 'Programs\NusaShell-Electron'
  }
  $versions = Join-Path $root 'versions'
  $target = Join-Path $versions $version
  $current = Join-Path $root 'current'
  New-Item -ItemType Directory -Force -Path $versions | Out-Null
  $previous = Get-PreviousVersion $current
  if (Test-Path -LiteralPath $target) { Remove-Item -LiteralPath $target -Recurse -Force }
  New-Item -ItemType Directory -Force -Path $target | Out-Null
  Get-ChildItem -LiteralPath $buildDir -Force | Copy-Item -Destination $target -Recurse -Force
  if (-not (Test-Path -LiteralPath (Join-Path $target 'nusashell-desktop.exe') -PathType Leaf)) {
    throw "Local Electron package did not install nusashell-desktop.exe."
  }

  Set-CurrentJunction $current $target
  Remove-OldVersions $versions $version $previous

  $shell = New-Object -ComObject WScript.Shell
  $shortcutPaths = @(
    (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\NusaShell-Desktop.lnk'),
    (Join-Path ([Environment]::GetFolderPath('Desktop')) 'NusaShell-Desktop.lnk')
  )
  foreach ($shortcutPath in $shortcutPaths) {
    New-Item -ItemType Directory -Force -Path (Split-Path $shortcutPath) | Out-Null
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = Join-Path $current 'nusashell-desktop.exe'
    $shortcut.WorkingDirectory = $current
    $shortcut.IconLocation = "$(Join-Path $current 'nusashell-desktop.exe'),0"
    $shortcut.Save()
  }
  Write-Host "Installed NusaShell Electron wrapper $version from checkout."
}

if ($ElectronOnly) {
  Install-ElectronFromBuild
  return
}

$installServiceSelected = Get-OptionalChoice $serviceOverride 'Install nusashell as a login service (autostart)?' 'NUSASHELL_INSTALL_SERVICE'
$installElectronSelected = Get-OptionalChoice $electronOverride 'Build and install Electron desktop wrapper?' 'NUSASHELL_INSTALL_ELECTRON'

$goVersion = (Get-Content -LiteralPath (Join-Path $repoRoot 'VERSION') -Raw).Trim()
if ($goVersion -notmatch $semverPattern) { throw "Invalid VERSION: $goVersion" }

$goRoot = if ($env:NUSASHELL_WINDOWS_GO_INSTALL_ROOT) {
  [IO.Path]::GetFullPath($env:NUSASHELL_WINDOWS_GO_INSTALL_ROOT)
} else {
  Join-Path $env:LOCALAPPDATA 'Programs\NusaShell'
}
$goVersions = Join-Path $goRoot 'versions'
$goTarget = Join-Path $goVersions $goVersion
$goCurrent = Join-Path $goRoot 'current'
New-Item -ItemType Directory -Force -Path $goVersions | Out-Null
$goPrevious = Get-PreviousVersion $goCurrent

$staging = Join-Path $goVersions ('.staging-' + [guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $staging | Out-Null
try {
  $builtExe = Join-Path $staging 'nusashell.exe'
  if ($env:NUSASHELL_LOCAL_GO_BINARY) {
    $source = [IO.Path]::GetFullPath($env:NUSASHELL_LOCAL_GO_BINARY)
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
      throw "NUSASHELL_LOCAL_GO_BINARY not found: $source"
    }
    Copy-Item -LiteralPath $source -Destination $builtExe -Force
  } else {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
      throw 'Go is required to build the NusaShell core from this checkout.'
    }
    Write-Host "Building Go core $goVersion…"
    Push-Location $repoRoot
    try {
      & go build -buildvcs=false "-ldflags=-X main.version=$goVersion" -o $builtExe ./cmd/nusashell
      if ($LASTEXITCODE -ne 0) { throw 'Go core build failed.' }
    } finally {
      Pop-Location
    }
  }
  if (-not (Test-Path -LiteralPath $builtExe -PathType Leaf)) {
    throw 'Built nusashell.exe is missing.'
  }
  if (Test-Path -LiteralPath $goTarget) { Remove-Item -LiteralPath $goTarget -Recurse -Force }
  Move-Item -LiteralPath $staging -Destination $goTarget
} finally {
  if (Test-Path -LiteralPath $staging) { Remove-Item -LiteralPath $staging -Recurse -Force -ErrorAction SilentlyContinue }
}

Set-CurrentJunction $goCurrent $goTarget
Remove-OldVersions $goVersions $goVersion $goPrevious

$launcher = Join-Path $goRoot 'nusashell.cmd'
Set-Content -LiteralPath $launcher -Encoding ascii -Value @('@echo off', '"%~dp0current\nusashell.exe" %*')
Write-Host "Installed NusaShell Go core $goVersion from checkout. Run: $launcher"

$shell = New-Object -ComObject WScript.Shell
$startMenuPrograms = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
$coreShortcutPath = Join-Path $startMenuPrograms 'NusaShell.lnk'
New-Item -ItemType Directory -Force -Path (Split-Path $coreShortcutPath) | Out-Null
$coreShortcut = $shell.CreateShortcut($coreShortcutPath)
$coreShortcut.TargetPath = $launcher
$coreShortcut.WorkingDirectory = $goRoot
$coreShortcut.IconLocation = "$(Join-Path $goCurrent 'nusashell.exe'),0"
$coreShortcut.Save()

if ($installServiceSelected) {
  & (Join-Path $goCurrent 'nusashell.exe') service install
  if ($LASTEXITCODE -eq 0) {
    Write-Host 'NusaShell starts automatically at login. Manage it with: nusashell service status'
  } else {
    Write-Warning 'NusaShell service install failed; run "nusashell service install" manually for details.'
  }
}

if ($installElectronSelected) {
  Write-Host 'Packaging Electron desktop wrapper…'
  Push-Location (Join-Path $repoRoot 'apps\electron')
  try {
    & npm run package:dir
    if ($LASTEXITCODE -ne 0) { throw 'Electron package failed.' }
  } finally {
    Pop-Location
  }
  Install-ElectronFromBuild
}
