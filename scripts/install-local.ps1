[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$version = (Get-Content -LiteralPath (Join-Path $repoRoot 'apps\electron\VERSION') -Raw).Trim()
$semverPattern = '^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$'
if ($version -notmatch $semverPattern) { throw "Invalid apps/electron/VERSION: $version" }
$buildDir = if ($env:NUSASHELL_BUILD_DIR) {
  [IO.Path]::GetFullPath($env:NUSASHELL_BUILD_DIR)
} else {
  Join-Path $repoRoot 'apps\electron\dist\win-unpacked'
}
if (-not (Test-Path -LiteralPath $buildDir -PathType Container)) {
  throw "Build output not found at $buildDir. Run make electron-package first or set NUSASHELL_BUILD_DIR."
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
if (Test-Path -LiteralPath $target) { Remove-Item -LiteralPath $target -Recurse -Force }
New-Item -ItemType Directory -Force -Path $target | Out-Null
Get-ChildItem -LiteralPath $buildDir -Force | Copy-Item -Destination $target -Recurse -Force
if (-not (Test-Path -LiteralPath (Join-Path $target 'nusashell-desktop.exe') -PathType Leaf)) {
  throw "Local Electron package did not install nusashell-desktop.exe."
}

if (Test-Path -LiteralPath $current) {
  $currentItem = Get-Item -LiteralPath $current -Force
  if (($currentItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    [IO.Directory]::Delete($current)
  } else {
    Remove-Item -LiteralPath $current -Recurse -Force
  }
}
New-Item -ItemType Junction -Path $current -Target $target | Out-Null

Get-ChildItem -LiteralPath $versions -Directory |
  Where-Object { $_.Name -ne $version } |
  ForEach-Object {
    try { Remove-Item -LiteralPath $_.FullName -Recurse -Force }
    catch { Write-Warning "Keeping old version $($_.Name): $($_.Exception.Message)" }
  }

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
Write-Host "Installed NusaShell Electron wrapper $version from $buildDir."
