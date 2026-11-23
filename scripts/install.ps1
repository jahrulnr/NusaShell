<#
.SYNOPSIS
    NusaShell installer for Windows.

.DESCRIPTION
    Downloads the latest NusaShell release binary for this machine,
    verifies it against the published SHA256SUMS, then runs its
    `serve install`, which copies the binary to
    %LOCALAPPDATA%\NusaShell\bin\nusashell.exe, registers a hidden logon
    task, and adds a Start Menu entry that opens the WebUI.

    Quick install (latest release):
        irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex

    Binary only, no scheduled task:
        powershell -ExecutionPolicy Bypass -File install.ps1 -NoService

    Uninstall (keeps config and data):
        powershell -ExecutionPolicy Bypass -File install.ps1 -Uninstall
#>
[CmdletBinding()]
param(
    [string]$Version,
    [switch]$NoService,
    [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# $IsWindows is $null on Windows PowerShell 5.1, so only throw when it is
# explicitly $false (PowerShell Core on non-Windows).
if ($IsWindows -eq $false) {
    throw 'install.ps1 supports Windows only; on Linux run: curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash'
}

$repo = 'jahrulnr/NusaShell'
$taskName = 'nusashell'
$installDir = Join-Path $env:LOCALAPPDATA 'NusaShell\bin'
$binary = Join-Path $installDir 'nusashell.exe'
$configDir = Join-Path $env:APPDATA 'nusashell'
$dataDir = Join-Path $env:LOCALAPPDATA 'nusashell'
$shortcut = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\NusaShell.url'

function Get-ReleaseUrl([string]$Asset) {
    if ([string]::IsNullOrWhiteSpace($Version) -or $Version -eq 'latest') {
        "https://github.com/$repo/releases/latest/download/$Asset"
    } else {
        # CI tags bare semver (0.1.0), no v prefix.
        $tag = $Version.TrimStart('v')
        "https://github.com/$repo/releases/download/$tag/$Asset"
    }
}

# Wait until the installed image accepts a writer — a running .exe is
# locked against overwrite, and Task Scheduler lets go of it a moment
# after the task ends.
function Wait-Installable([string]$Path) {
    for ($i = 0; $i -lt 60; $i++) {
        if (-not (Test-Path -LiteralPath $Path)) { return }
        try {
            $probe = [IO.File]::Open($Path, 'Open', 'ReadWrite', 'None')
            $probe.Close()
            return
        } catch {
            Start-Sleep -Milliseconds 250
        }
    }
    throw "$Path is still in use by a running process - close it and re-run"
}

if ($Uninstall) {
    schtasks /End /TN $taskName 2>$null | Out-Null
    schtasks /Delete /TN $taskName /F 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) { Write-Host "Removed scheduled task '$taskName'." }
    Wait-Installable $binary
    if (Test-Path -LiteralPath $binary) {
        Remove-Item -LiteralPath $binary -Force
        Write-Host "Removed $binary"
    }
    # Stale images parked by an earlier self-uninstall go too.
    Remove-Item -Path "$binary.old-*" -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $shortcut -Force -ErrorAction SilentlyContinue
    Write-Host "Kept config $configDir"
    Write-Host "Kept data   $dataDir"
    Write-Host 'Delete them manually to purge.'
    # return instead of exit: exit would terminate the caller's session
    # when the script is invoked with & or piped through iex.
    return
}

# A 32-bit PowerShell host on ARM64 reports x86 — ARCHITEW6432 names the
# real machine arch then.
$procArch = $env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrWhiteSpace($procArch)) {
    $procArch = $env:PROCESSOR_ARCHITECTURE
}
if ([string]::IsNullOrWhiteSpace($procArch)) {
    $procArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
}
# PROCESSOR_ARCHITECTURE says AMD64/ARM64; OSArchitecture says X64/Arm64.
switch ($procArch.ToUpperInvariant()) {
    { $_ -in 'AMD64', 'X64' } { $arch = 'amd64'; break }
    { $_ -in 'ARM64' }        { $arch = 'arm64'; break }
    default { throw "unsupported architecture: $procArch" }
}

$assetName = "nusashell-windows-$arch.exe"
$tmp = Join-Path $env:TEMP ("nusashell-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $binUrl = Get-ReleaseUrl $assetName
    $sumUrl = Get-ReleaseUrl 'SHA256SUMS'
    Write-Host "nusashell install: downloading $assetName"
    Invoke-WebRequest -Uri $binUrl -OutFile (Join-Path $tmp $assetName) -UseBasicParsing
    Invoke-WebRequest -Uri $sumUrl -OutFile (Join-Path $tmp 'SHA256SUMS') -UseBasicParsing

    $expected = Get-Content (Join-Path $tmp 'SHA256SUMS') |
        Where-Object { ($_ -split '\s+')[-1] -eq $assetName } |
        ForEach-Object { ($_ -split '\s+')[0] } |
        Select-Object -First 1
    if (-not $expected) { throw "no checksum found for $assetName in SHA256SUMS" }
    $actual = (Get-FileHash (Join-Path $tmp $assetName) -Algorithm SHA256).Hash
    if ($actual -ne $expected) { throw "checksum mismatch for $assetName" }
    Write-Host 'nusashell install: checksum verified (SHA-256)'

    # The downloaded binary installs itself: serve install copies it to
    # %LOCALAPPDATA%\NusaShell\bin, registers the logon task, and writes
    # the Start Menu entry. A running task is stopped there, the same
    # way install.sh works on Linux.
    $installArgs = @('serve', 'install')
    if ($NoService) { $installArgs += '--no-service' }
    & (Join-Path $tmp $assetName) @installArgs
    if ($LASTEXITCODE -ne 0) { throw "serve install exited with code $LASTEXITCODE" }
}
finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host 'NusaShell is installed.'
Write-Host "  binary: $binary"
Write-Host "  data:   $dataDir"
if (-not $NoService) {
    Write-Host "  task:   $taskName (logon autostart)"
    Write-Host '  stop:   schtasks /End /TN nusashell'
    Write-Host '  start:  schtasks /Run /TN nusashell'
    Write-Host '  uninstall: nusashell uninstall'
}
