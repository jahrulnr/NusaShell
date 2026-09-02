[CmdletBinding()]
param(
  [string]$Version = '',
  [string]$ElectronVersion = '',
  [switch]$InstallElectron,
  [switch]$NoElectron,
  [switch]$InstallMcp,
  [switch]$NoMcp
)

# Install the NusaShell Go core and optional Electron/MCP components for the
# current Windows user. No administrator rights are required.
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$requestedVersion = if ($Version) { $Version } elseif ($env:NUSASHELL_VERSION) { $env:NUSASHELL_VERSION } else { '' }
$requestedElectronVersion = if ($ElectronVersion) { $ElectronVersion } elseif ($env:NUSASHELL_ELECTRON_VERSION) { $env:NUSASHELL_ELECTRON_VERSION } else { '' }
$electronOverride = if ($InstallElectron) { '1' } elseif ($NoElectron) { '0' } elseif ($env:NUSASHELL_INSTALL_ELECTRON) { $env:NUSASHELL_INSTALL_ELECTRON } else { '' }
$mcpOverride = if ($InstallMcp) { '1' } elseif ($NoMcp) { '0' } elseif ($env:NUSASHELL_INSTALL_MCP) { $env:NUSASHELL_INSTALL_MCP } else { '' }
$semverPattern = '^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$'
if ($requestedVersion -and $requestedVersion -notmatch $semverPattern) {
  throw "Invalid release version: $requestedVersion"
}
if ($requestedElectronVersion -and $requestedElectronVersion -notmatch $semverPattern) {
  throw "Invalid Electron release version: $requestedElectronVersion"
}

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

$installElectronSelected = Get-OptionalChoice $electronOverride 'Install Electron desktop wrapper?' 'NUSASHELL_INSTALL_ELECTRON'
$installMcpSelected = Get-OptionalChoice $mcpOverride 'Install MCP plugins from NusaShell-mcp?' 'NUSASHELL_INSTALL_MCP'

$repo = if ($env:NUSASHELL_REPOSITORY) { $env:NUSASHELL_REPOSITORY } else { 'jahrulnr/NusaShell' }
$base = if ($env:NUSASHELL_RELEASE_BASE) { $env:NUSASHELL_RELEASE_BASE.TrimEnd('/') } else { "https://github.com/$repo/releases" }
if ($base -notmatch '^https://') { throw 'NUSASHELL_RELEASE_BASE must use HTTPS.' }
$releaseIndexUrl = if ($env:NUSASHELL_RELEASE_INDEX) { $env:NUSASHELL_RELEASE_INDEX } else { "https://raw.githubusercontent.com/$repo/master/release-versions.json" }
$temp = Join-Path ([IO.Path]::GetTempPath()) ("nusashell-install-" + [guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $temp | Out-Null

function Download-File([string]$Url, [string]$Destination) {
  if ($Url -notmatch '^https://') { throw "Refusing non-HTTPS download URL: $Url" }
  Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Destination
}

function Get-Manifest([string]$AssetName, [string]$Destination, [string]$Tag) {
  $url = "$base/download/$Tag/$AssetName"
  Download-File $url $Destination
  return (Get-Content -LiteralPath $Destination -Raw | ConvertFrom-Json)
}

$releaseIndex = $null
function Get-StreamRelease([string]$Stream, [string]$Requested) {
  $manifestName = if ($Stream -eq 'go') { 'latest.json' } else { 'electron-latest.json' }
  if ($Requested) {
    $version = $Requested
    $tag = "$Stream-v$version"
  } else {
    if (-not $script:releaseIndex) {
      $indexPath = Join-Path $temp 'release-versions.json'
      Download-File $releaseIndexUrl $indexPath
      $script:releaseIndex = Get-Content -LiteralPath $indexPath -Raw | ConvertFrom-Json
    }
    $entry = $script:releaseIndex.PSObject.Properties[$Stream].Value
    if (-not $entry) { throw "No published NusaShell $Stream release is available." }
    $version = [string]$entry.version
    $tag = [string]$entry.tag
    if ($entry.manifest) { $manifestName = [string]$entry.manifest }
  }
  if ($version -notmatch $semverPattern) { throw "$Stream release index contains an invalid version." }
  if ($tag -ne "$Stream-v$version" -or $tag -notmatch '^[A-Za-z0-9][A-Za-z0-9._+-]*$') {
    throw "$Stream release index contains an unsafe tag."
  }
  if ($manifestName -ne $(if ($Stream -eq 'go') { 'latest.json' } else { 'electron-latest.json' })) {
    throw "$Stream release index contains an invalid manifest name."
  }
  return [pscustomobject]@{ Version = $version; Tag = $tag; Manifest = $manifestName }
}

function Assert-SafeFileName([string]$Name) {
  if ([IO.Path]::GetFileName($Name) -ne $Name -or $Name -match '[\\/]') {
    throw "Release payload name is unsafe: $Name"
  }
}

function Assert-SafeZip([string]$Archive) {
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
  try {
    foreach ($entry in $zip.Entries) {
      $name = $entry.FullName.Replace('/', '\')
      if ([IO.Path]::IsPathRooted($name) -or $name.Split('\') -contains '..' -or $name.StartsWith('..\')) {
        throw "Release archive entry is unsafe: $($entry.FullName)"
      }
    }
  } finally {
    $zip.Dispose()
  }
}

function Install-ZipPayload([string]$Archive, [string]$Versions, [string]$Target, [string]$Executable) {
  Assert-SafeZip $Archive
  $unpacked = Join-Path $temp ("unpacked-" + [guid]::NewGuid().ToString())
  Expand-Archive -LiteralPath $Archive -DestinationPath $unpacked
  $exe = Get-ChildItem -LiteralPath $unpacked -Filter $Executable -File -Recurse | Select-Object -First 1
  if (-not $exe) { throw "The release did not contain $Executable." }
  if ((Test-Path -LiteralPath $Target) -and -not (Test-Path -LiteralPath (Join-Path $Target $Executable) -PathType Leaf)) {
    Remove-Item -LiteralPath $Target -Recurse -Force
  }
  if (-not (Test-Path -LiteralPath $Target)) {
    $staging = Join-Path $Versions ('.staging-' + [guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Force -Path $staging | Out-Null
    try {
      Get-ChildItem -LiteralPath $exe.Directory.FullName -Force | Copy-Item -Destination $staging -Recurse -Force
      Move-Item -LiteralPath $staging -Destination $Target
    } finally {
      if (Test-Path -LiteralPath $staging) { Remove-Item -LiteralPath $staging -Recurse -Force -ErrorAction SilentlyContinue }
    }
  }
  if (-not (Test-Path -LiteralPath (Join-Path $Target $Executable) -PathType Leaf)) {
    throw "The release did not install $Executable into $Target."
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

function Get-PreviousVersion([string]$Current) {
  if (-not (Test-Path -LiteralPath $Current)) { return '' }
  try {
    return Split-Path ((Resolve-Path -LiteralPath $Current).Path.TrimEnd('\')) -Leaf
  } catch {
    return ''
  }
}

function Remove-OldVersions([string]$Versions, [string]$Active, [string]$Previous) {
  Get-ChildItem -LiteralPath $Versions -Directory |
    Where-Object { $_.Name -notin @($Active, $Previous) -and $_.Name -notlike '.staging-*' } |
    ForEach-Object {
      try { Remove-Item -LiteralPath $_.FullName -Recurse -Force }
      catch { Write-Warning "Keeping old version $($_.Name): $($_.Exception.Message)" }
    }
}

function Get-Checksum([string]$Path) {
  return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

try {
  $manifestPath = Join-Path $temp 'latest.json'
  $goRelease = Get-StreamRelease 'go' $requestedVersion
  $manifest = Get-Manifest $goRelease.Manifest $manifestPath $goRelease.Tag
  $resolvedVersion = [string]$manifest.version
  if ($resolvedVersion -notmatch $semverPattern) { throw 'Release manifest contains an invalid version.' }
  if ($resolvedVersion -ne $goRelease.Version) {
    throw "Go release manifest version $resolvedVersion does not match release index $($goRelease.Version)."
  }

  $entry = $manifest.files.PSObject.Properties['win32-x64'].Value
  if (-not $entry) { throw 'No Windows x64 Go payload is published for this release.' }
  $fileName = [string]$entry.name
  $expectedSha = [string]$entry.sha256
  Assert-SafeFileName $fileName
  if ($expectedSha -notmatch '^[0-9a-fA-F]{64}$') { throw 'Release manifest contains an invalid SHA-256 digest.' }
  $archive = Join-Path $temp $fileName
  Download-File "$base/download/$($goRelease.Tag)/$fileName" $archive
  if ((Get-Checksum $archive) -ne $expectedSha.ToLowerInvariant()) { throw 'Checksum verification failed; refusing to install.' }

  $root = if ($env:NUSASHELL_WINDOWS_GO_INSTALL_ROOT) {
    [IO.Path]::GetFullPath($env:NUSASHELL_WINDOWS_GO_INSTALL_ROOT)
  } else {
    Join-Path $env:LOCALAPPDATA 'Programs\NusaShell'
  }
  $versions = Join-Path $root 'versions'
  $target = Join-Path $versions $resolvedVersion
  $current = Join-Path $root 'current'
  New-Item -ItemType Directory -Force -Path $versions | Out-Null
  $previous = Get-PreviousVersion $current
  Install-ZipPayload $archive $versions $target 'nusashell.exe'
  Set-CurrentJunction $current $target
  Remove-OldVersions $versions $resolvedVersion $previous
  $launcher = Join-Path $root 'nusashell.cmd'
  Set-Content -LiteralPath $launcher -Encoding ascii -Value @('@echo off', '"%~dp0current\nusashell.exe" %*')
  Write-Host "Installed NusaShell Go core $resolvedVersion. Run: $launcher"

  if ($installElectronSelected) {
    $electronManifestPath = Join-Path $temp 'electron-latest.json'
    $electronRelease = Get-StreamRelease 'electron' $requestedElectronVersion
    $electronManifest = Get-Manifest $electronRelease.Manifest $electronManifestPath $electronRelease.Tag
    $electronVersion = [string]$electronManifest.version
    if ($electronVersion -ne $electronRelease.Version) { throw "Electron release manifest version $electronVersion does not match release index $($electronRelease.Version)." }
    $electronEntry = $electronManifest.files.PSObject.Properties['win32-x64'].Value
    if (-not $electronEntry) { throw 'No Windows x64 Electron payload is published for this release.' }
    $electronName = [string]$electronEntry.name
    $electronSha = [string]$electronEntry.sha256
    Assert-SafeFileName $electronName
    if ($electronSha -notmatch '^[0-9a-fA-F]{64}$') { throw 'Electron manifest contains an invalid SHA-256 digest.' }
    $electronArchive = Join-Path $temp $electronName
    Download-File "$base/download/$($electronRelease.Tag)/$electronName" $electronArchive
    if ((Get-Checksum $electronArchive) -ne $electronSha.ToLowerInvariant()) { throw 'Electron checksum verification failed; refusing to install.' }

    $electronRoot = if ($env:NUSASHELL_WINDOWS_ELECTRON_INSTALL_ROOT) {
      [IO.Path]::GetFullPath($env:NUSASHELL_WINDOWS_ELECTRON_INSTALL_ROOT)
    } else {
      Join-Path $env:LOCALAPPDATA 'Programs\NusaShell-Electron'
    }
    $electronVersions = Join-Path $electronRoot 'versions'
    $electronTarget = Join-Path $electronVersions $electronVersion
    $electronCurrent = Join-Path $electronRoot 'current'
    New-Item -ItemType Directory -Force -Path $electronVersions | Out-Null
    $electronPrevious = Get-PreviousVersion $electronCurrent
    Install-ZipPayload $electronArchive $electronVersions $electronTarget 'nusashell-desktop.exe'
    Set-CurrentJunction $electronCurrent $electronTarget
    Remove-OldVersions $electronVersions $electronVersion $electronPrevious

    $shell = New-Object -ComObject WScript.Shell
    $shortcutPaths = @(
      (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\NusaShell-Desktop.lnk'),
      (Join-Path ([Environment]::GetFolderPath('Desktop')) 'NusaShell-Desktop.lnk')
    )
    foreach ($shortcutPath in $shortcutPaths) {
      New-Item -ItemType Directory -Force -Path (Split-Path $shortcutPath) | Out-Null
      $shortcut = $shell.CreateShortcut($shortcutPath)
      $shortcut.TargetPath = Join-Path $electronCurrent 'nusashell-desktop.exe'
      $shortcut.WorkingDirectory = $electronCurrent
      $shortcut.IconLocation = "$(Join-Path $electronCurrent 'nusashell-desktop.exe'),0"
      $shortcut.Save()
    }
    Write-Host "Installed NusaShell Electron wrapper $electronVersion."
  }

  if ($installMcpSelected) {
    $mcpRepo = if ($env:NUSASHELL_MCP_REPOSITORY) { $env:NUSASHELL_MCP_REPOSITORY } elseif ($env:NUSASHELL_MCP_REPO) { $env:NUSASHELL_MCP_REPO } else { 'jahrulnr/NusaShell-mcp' }
    $rawBase = if ($env:NUSASHELL_MCP_RAW_BASE) { $env:NUSASHELL_MCP_RAW_BASE } else { "https://raw.githubusercontent.com/$mcpRepo/master" }
    $archiveBase = if ($env:NUSASHELL_MCP_ARCHIVE_BASE) { $env:NUSASHELL_MCP_ARCHIVE_BASE.TrimEnd('/') } else { "https://github.com/$mcpRepo/archive/refs/tags" }
    $catalogPath = Join-Path $temp 'mcp-versions.json'
    Download-File "$rawBase/versions.json" $catalogPath
    $catalog = Get-Content -LiteralPath $catalogPath -Raw | ConvertFrom-Json
    $pluginKeys = if ($env:NUSASHELL_MCP_PLUGINS) { $env:NUSASHELL_MCP_PLUGINS -replace ',', ' ' } else { 'kanban notes whatsapp telegram' }
    $dataDir = if ($env:NUSASHELL_DATA_DIR) { [IO.Path]::GetFullPath($env:NUSASHELL_DATA_DIR) } else { Join-Path $env:APPDATA 'nusashell' }
    $pluginsRoot = Join-Path $dataDir 'plugins'
    New-Item -ItemType Directory -Force -Path $pluginsRoot | Out-Null

    foreach ($plugin in ($pluginKeys -split '\s+' | Where-Object { $_ })) {
      $info = $catalog.PSObject.Properties[$plugin].Value
      if (-not $info) { throw "NusaShell-mcp catalog has no entry for $plugin." }
      $tag = [string]$info.tag
      $pluginVersion = [string]$info.version
      if (-not $tag -or -not $pluginVersion) { throw "NusaShell-mcp catalog entry for $plugin is incomplete." }
      if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw "MCP plugin $plugin requires Go on Windows because native NusaShell-mcp assets are not published yet."
      }

      $pluginTemp = Join-Path $temp ("mcp-$plugin")
      $sourceArchive = Join-Path $temp ("mcp-$plugin-source.zip")
      New-Item -ItemType Directory -Force -Path $pluginTemp | Out-Null
      Download-File "$archiveBase/$tag.zip" $sourceArchive
      Assert-SafeZip $sourceArchive
      $sourceExtracted = Join-Path $pluginTemp 'source'
      Expand-Archive -LiteralPath $sourceArchive -DestinationPath $sourceExtracted
      $manifestFile = Get-ChildItem -LiteralPath $sourceExtracted -Filter 'manifest.json' -File -Recurse |
        Where-Object { Test-Path (Join-Path $_.Directory.FullName 'mcp') } | Select-Object -First 1
      if (-not $manifestFile) { throw "NusaShell-mcp archive has no manifest for $plugin." }
      $pluginSource = $manifestFile.Directory.FullName
      Push-Location (Join-Path $pluginSource 'mcp')
      try { & go build -buildvcs=false -o server.exe . } finally { Pop-Location }
      if ($LASTEXITCODE -ne 0) { throw "Could not build MCP plugin $plugin for Windows." }
      $pluginManifest = Get-Content -LiteralPath $manifestFile.FullName -Raw | ConvertFrom-Json
      $pluginId = [string]$pluginManifest.id
      if ($pluginId -notmatch '^[A-Za-z0-9._-]+$') { throw "MCP plugin $plugin has an invalid manifest id." }
      $destination = Join-Path $pluginsRoot $pluginId
      if (Test-Path -LiteralPath $destination) { Remove-Item -LiteralPath $destination -Recurse -Force }
      New-Item -ItemType Directory -Force -Path $destination | Out-Null
      Get-ChildItem -LiteralPath $pluginSource -Force | Copy-Item -Destination $destination -Recurse -Force
      Write-Host "Installed MCP plugin: $pluginId $pluginVersion"
    }
    Write-Host "NusaShell-mcp plugins are installed under $pluginsRoot."
  }
} finally {
  Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
}
