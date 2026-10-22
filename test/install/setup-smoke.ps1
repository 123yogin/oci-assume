# Windows CI smoke: install.ps1 via iex from a snapshot fixture, then setup and uninstall for real.
# Usage: setup-smoke.ps1 -Dist <goreleaser dist dir>. Changes the runner's profile, PATH and Task Scheduler.
param([Parameter(Mandatory)][string]$Dist)
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path (Join-Path $PSScriptRoot '..') '..')).Path
function Fail($m) { Write-Error "FAIL: $m"; exit 1 }
function Pass($m) { Write-Host "ok   $m" }

# Package the snapshot binaries as a release install.ps1 accepts (it wants vX.Y.Z names).
$arch = if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$rel = Join-Path $env:RUNNER_TEMP 'rel'
$pkg = Join-Path $env:RUNNER_TEMP 'pkg'
New-Item -ItemType Directory -Force -Path $rel, $pkg | Out-Null
foreach ($exe in 'oci-assume.exe', 'oci-assume-bg.exe') {
    $src = Get-ChildItem -Recurse -Path $Dist -Filter $exe | Where-Object { $_.DirectoryName -match "windows_$arch" } | Select-Object -First 1
    if (-not $src) { Fail "snapshot has no $exe for $arch" }
    Copy-Item $src.FullName (Join-Path $pkg $exe)
}
$archive = "oci-assume_9.9.9_windows_$arch.zip"
Compress-Archive -Path (Join-Path $pkg '*') -DestinationPath (Join-Path $rel $archive)
$h = (Get-FileHash -Algorithm SHA256 (Join-Path $rel $archive)).Hash.ToLower()
Set-Content -Path (Join-Path $rel 'checksums.txt') -Value "$h  $archive"

# The irm | iex path: the script text runs in this session, configured by env.
$env:OCI_ASSUME_BASE_URL = (New-Object Uri $rel).AbsoluteUri
$env:OCI_ASSUME_VERSION = 'v9.9.9'
$env:OCI_ASSUME_NO_SETUP = '1'
Get-Content -Raw (Join-Path $root 'install.ps1') | Invoke-Expression
Remove-Item Env:\OCI_ASSUME_NO_SETUP
$dest = Join-Path $env:LOCALAPPDATA 'Programs\oci-assume'
$helper = Join-Path $dest 'oci-assume-bg.exe'
if (-not (Get-Command oci-assume.exe -ErrorAction SilentlyContinue)) { Fail 'oci-assume not on PATH after install' }
if (-not (Test-Path $helper)) { Fail "helper missing: $helper" }
Pass 'install.ps1 via iex'

$profiles = @()
foreach ($ps in 'pwsh', 'powershell') {
    if (Get-Command $ps -ErrorAction SilentlyContinue) {
        $profiles += (& $ps -NoProfile -Command '$PROFILE.CurrentUserAllHosts').Trim()
    }
}
function Test-Block($p) { (Test-Path $p) -and ((Get-Content -Raw $p) -match '# >>> oci-assume >>>') }
function Get-TaskXml { $x = schtasks /Query /TN oci-assume-refresh /XML 2>$null | Out-String; if ($LASTEXITCODE -eq 0) { $x } else { $null } }

oci-assume setup --yes --no-oci-cli --no-login
if ($LASTEXITCODE -ne 0) { Fail "setup exit $LASTEXITCODE" }
foreach ($p in $profiles) { if (-not (Test-Block $p)) { Fail "no block in $p" } }
Pass "profile block in $($profiles -join ', ')"
$xml = Get-TaskXml
if (-not $xml) { Fail 'task not registered' }
if ($xml -notmatch '<LogonTrigger>') { Fail "task has no logon trigger:`n$xml" }
if ($xml -notmatch [regex]::Escape('oci-assume-bg.exe')) { Fail "task does not run the helper:`n$xml" }
Pass 'task with logon trigger runs the helper'

oci-assume uninstall
if ($LASTEXITCODE -ne 0) { Fail "uninstall exit $LASTEXITCODE" }
foreach ($p in $profiles) { if (Test-Block $p) { Fail "block left in $p" } }
if (Get-TaskXml) { Fail 'task still registered' }
Pass 'uninstall removed the profile blocks and the task'
# The helper exe belongs to the install, not setup; uninstall only prints how to remove binaries.
