# Tests install.ps1 against a file:// fixture. Registry cases run on Windows only.
# Needs Go on PATH to build a fake oci-assume that logs its setup args.
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path (Join-Path $PSScriptRoot '..') '..')).Path
$script = Join-Path $root 'install.ps1'
$T = Join-Path ([IO.Path]::GetTempPath()) ("oa-test-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $T | Out-Null
$isWin = ($PSVersionTable.PSVersion.Major -lt 6) -or $IsWindows
$pwsh = (Get-Process -Id $PID).Path

function Fail($m) { Write-Error "FAIL: $m"; exit 1 }

# Load the helper functions without running the installer.
$ast = [Management.Automation.Language.Parser]::ParseFile($script, [ref]$null, [ref]$null)
foreach ($f in $ast.FindAll({ param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] }, $true)) {
    if ($f.Name -ne 'Install-OciAssume') { . ([scriptblock]::Create($f.Extent.Text)) }
}

# Fake binary: logs "setup <args>" to $SETUP_LOG and exits with $FAKE_SETUP_EXIT.
$src = Join-Path $T 'fake'
New-Item -ItemType Directory -Path $src | Out-Null
Set-Content -Path (Join-Path $src 'go.mod') -Value "module fake`ngo 1.21"
Set-Content -Path (Join-Path $src 'main.go') -Value @'
package main

import (
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		f, _ := os.OpenFile(os.Getenv("SETUP_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		f.WriteString(strings.Join(os.Args[1:], " ") + " inst=" + os.Getenv("OCI_ASSUME_INSTALLER") + "\n")
		f.Close()
		n, _ := strconv.Atoi(os.Getenv("FAKE_SETUP_EXIT"))
		os.Exit(n)
	}
}
'@
$pkg = Join-Path $T 'pkg'
New-Item -ItemType Directory -Path $pkg | Out-Null
Push-Location $src
try { & go build -o (Join-Path $pkg 'oci-assume.exe') . } finally { Pop-Location }
if ($LASTEXITCODE -ne 0) { Fail 'go build of the fake binary failed' }

$arch = if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$archive = "oci-assume_9.9.9_windows_$arch.zip"
$rel = Join-Path $T 'rel'
New-Item -ItemType Directory -Path $rel | Out-Null
if ($isWin) {
    Compress-Archive -Path (Join-Path $pkg 'oci-assume.exe') -DestinationPath (Join-Path $rel $archive)
} else {
    # zip(1) keeps the exec bit so the fake can run on Unix.
    Push-Location $pkg; try { & zip -q (Join-Path $rel $archive) 'oci-assume.exe' } finally { Pop-Location }
}
function Write-Sums { $h = (Get-FileHash -Algorithm SHA256 (Join-Path $rel $archive)).Hash.ToLower(); [IO.File]::WriteAllText((Join-Path $rel 'checksums.txt'), "$h  $archive`n", (New-Object Text.UTF8Encoding $false)) }
Write-Sums
$orig = Join-Path $T 'orig.zip'; Copy-Item (Join-Path $rel $archive) $orig

$env:OCI_ASSUME_SKIP_PATH = '1'
$env:SETUP_LOG = Join-Path $T 'setup.log'
$env:OCI_ASSUME_BASE_URL = (New-Object Uri ((Resolve-Path $rel).Path)).AbsoluteUri

function Invoke-Install($dir, [string[]]$more = @()) {
    $env:OCI_ASSUME_INSTALL_DIR = $dir
    # 5.1 wraps redirected native stderr as errors; under Stop the expected failures would throw.
    $ErrorActionPreference = 'Continue'
    $out = & $pwsh -NoProfile -File $script -Version v9.9.9 @more 2>&1 | Out-String
    return @{ Code = $LASTEXITCODE; Out = $out }
}
function Exe($d) { Join-Path (Join-Path $T $d) 'oci-assume.exe' }

# 1: valid release installs; NoSetup prints the hint and does not run setup.
$r = Invoke-Install (Join-Path $T 'ok') @('-NoSetup')
if ($r.Code -ne 0) { Fail "valid release failed: $($r.Out)" }
if (-not (Test-Path (Exe 'ok'))) { Fail "binary missing: $($r.Out)" }
if ($r.Out -notmatch 'Next: oci-assume setup') { Fail "no Next hint: $($r.Out)" }
if (Test-Path $env:SETUP_LOG) { Fail 'setup ran with -NoSetup' }

# 2: tampered archive aborts, installs nothing.
[IO.File]::AppendAllText((Join-Path $rel $archive), 'junk')
$r = Invoke-Install (Join-Path $T 'bad') @('-NoSetup')
if ($r.Code -eq 0) { Fail 'tampered archive installed' }
if ($r.Out -notmatch 'checksum mismatch') { Fail "no mismatch message: $($r.Out)" }
if (Test-Path (Exe 'bad')) { Fail 'tampered install left a binary' }

# 3: missing checksum entry aborts.
Copy-Item $orig (Join-Path $rel $archive) -Force
[IO.File]::WriteAllText((Join-Path $rel 'checksums.txt'), '', (New-Object Text.UTF8Encoding $false))
$r = Invoke-Install (Join-Path $T 'none') @('-NoSetup')
if ($r.Code -eq 0) { Fail 'missing checksum entry installed' }
if ($r.Out -notmatch 'no checksum entry for') { Fail "no missing-entry message: $($r.Out)" }
Write-Sums

# 4: bad version rejected.
$env:OCI_ASSUME_INSTALL_DIR = Join-Path $T 'v'
& { $ErrorActionPreference = 'Continue'; & $pwsh -NoProfile -File $script -Version v1.2 -NoSetup *> $null }
if ($LASTEXITCODE -eq 0) { Fail 'bad version accepted' }

# 5: setup handoff, -Yes and env passthrough.
$r = Invoke-Install (Join-Path $T 's1')
if ($r.Code -ne 0) { Fail "setup install failed: $($r.Out)" }
if (-not (Test-Path $env:SETUP_LOG)) { Fail "setup did not run: $($r.Out)" }
if ((Get-Content $env:SETUP_LOG) -ne 'setup inst=1') { Fail "setup args wrong: $($r.Out) $(Get-Content $env:SETUP_LOG)" }
Remove-Item $env:SETUP_LOG
$r = Invoke-Install (Join-Path $T 's2') @('-Yes')
if ((Get-Content $env:SETUP_LOG) -ne 'setup --yes inst=1') { Fail '-Yes not passed' }
Remove-Item $env:SETUP_LOG
$env:OCI_ASSUME_YES = '1'
$r = Invoke-Install (Join-Path $T 's3')
Remove-Item Env:\OCI_ASSUME_YES
if ((Get-Content $env:SETUP_LOG) -ne 'setup --yes inst=1') { Fail 'OCI_ASSUME_YES not passed' }
Remove-Item $env:SETUP_LOG

if ($env:OCI_ASSUME_INSTALLER) { Fail 'installer var leaked to the caller' }

# 6: a failing setup warns but does not fail the install.
$env:FAKE_SETUP_EXIT = '3'
$r = Invoke-Install (Join-Path $T 's4')
Remove-Item Env:\FAKE_SETUP_EXIT
if ($r.Code -ne 0) { Fail "failing setup masked a good install: $($r.Out)" }
if ($r.Out -notmatch 'setup did not finish') { Fail "no guidance: $($r.Out)" }
Remove-Item $env:SETUP_LOG

# 7: upgrade over an existing install renames the old exe, then cleans up next run.
$null = Invoke-Install (Join-Path $T 'ok') @('-NoSetup')
if (-not (Test-Path ((Exe 'ok') + '.old'))) { Fail 'no .old after upgrade' }
$null = Invoke-Install (Join-Path $T 'ok') @('-NoSetup')
if (-not (Test-Path (Exe 'ok'))) { Fail 'binary missing after second upgrade' }

# 8: running under iex leaks nothing into the caller.
$env:OCI_ASSUME_INSTALL_DIR = Join-Path $T 'iex'
$env:OCI_ASSUME_VERSION = 'v9.9.9'
$env:OCI_ASSUME_NO_SETUP = '1'
$ErrorActionPreference = 'Continue'
$Version = 'keep'; $NoSetup = 'keep'; $Yes = 'keep'
Get-Content $script -Raw | Invoke-Expression
if ($ErrorActionPreference -ne 'Continue') { Fail 'iex changed $ErrorActionPreference' }
if ($Version -ne 'keep' -or $NoSetup -ne 'keep' -or $Yes -ne 'keep') { Fail 'iex clobbered caller variables' }
if (Get-Command Install-OciAssume -ErrorAction SilentlyContinue) { Fail 'iex leaked Install-OciAssume' }
if (-not (Test-Path (Exe 'iex'))) { Fail 'iex install produced no binary' }
$ErrorActionPreference = 'Stop'
Remove-Item Env:\OCI_ASSUME_VERSION, Env:\OCI_ASSUME_NO_SETUP

# 9: PATH string logic keeps %VARS% literal and is idempotent.
$raw = '%USERPROFILE%\bin;C:\Tools'
$new = Get-AppendedPath -Raw $raw -Dir 'C:\oa'
if ($new -ne '%USERPROFILE%\bin;C:\Tools;C:\oa') { Fail "append wrong: $new" }
if ((Get-AppendedPath -Raw $new -Dir 'C:\oa\') -ne $new) { Fail 'append not idempotent' }
$env:OA_TEST_DIR = 'C:\oaexp'
if ((Get-AppendedPath -Raw '%OA_TEST_DIR%' -Dir 'C:\oaexp') -ne '%OA_TEST_DIR%') { Fail 'expanded duplicate not detected' }
Remove-Item Env:\OA_TEST_DIR

# 10: registry round-trip on Windows, in a scratch key.
if ($isWin) {
    $sub = 'Software\oci-assume-test'
    try {
        $k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($sub)
        $k.SetValue('Path', '%USERPROFILE%\bin', [Microsoft.Win32.RegistryValueKind]::ExpandString); $k.Close()
        Set-UserPathDir -Dir 'C:\oa' -SubKey $sub
        $k = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($sub)
        $v = $k.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $kind = $k.GetValueKind('Path'); $k.Close()
        if ($v -ne '%USERPROFILE%\bin;C:\oa') { Fail "registry path wrong: $v" }
        if ($kind -ne 'ExpandString') { Fail "registry kind $kind" }
        Send-EnvironmentChange
    } finally {
        [Microsoft.Win32.Registry]::CurrentUser.DeleteSubKeyTree($sub, $false)
    }
} else {
    Write-Host 'SKIP registry case (not Windows)'
}

Remove-Item -Recurse -Force $T
Write-Host 'PASS install.ps1'
