# Tests install.ps1 against a file:// fixture. Registry cases run on Windows only.
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
foreach ($f in $ast.FindAll({ param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] }, $false)) {
    if ($f.Name -ne 'Install-OciAssume') { . ([scriptblock]::Create($f.Extent.Text)) }
}

$arch = if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$archive = "oci-assume_9.9.9_windows_$arch.zip"
$rel = Join-Path $T 'rel'; $pkg = Join-Path $T 'pkg'
New-Item -ItemType Directory -Path $rel, $pkg | Out-Null
Set-Content -Path (Join-Path $pkg 'oci-assume.exe') -Value 'fake'
Compress-Archive -Path (Join-Path $pkg 'oci-assume.exe') -DestinationPath (Join-Path $rel $archive)
function Write-Sums { $h = (Get-FileHash -Algorithm SHA256 (Join-Path $rel $archive)).Hash.ToLower(); Set-Content -Path (Join-Path $rel 'checksums.txt') -Value "$h  $archive" }
Write-Sums
$orig = Join-Path $T 'orig.zip'; Copy-Item (Join-Path $rel $archive) $orig

function Invoke-Install($dir, [string[]]$more = @()) {
    $env:OCI_ASSUME_BASE_URL = (New-Object Uri ((Resolve-Path $rel).Path)).AbsoluteUri
    $env:OCI_ASSUME_INSTALL_DIR = $dir
    $out = & $pwsh -NoProfile -File $script -Version v9.9.9 @more 2>&1 | Out-String
    return @{ Code = $LASTEXITCODE; Out = $out }
}

# 1: valid release installs; NoSetup prints the hint.
$r = Invoke-Install (Join-Path $T 'ok') @('-NoSetup')
if ($r.Code -ne 0) { Fail "valid release failed: $($r.Out)" }
if (-not (Test-Path (Join-Path (Join-Path $T 'ok') 'oci-assume.exe'))) { Fail "binary missing: $($r.Out)" }
if ($r.Out -notmatch 'Next: oci-assume setup') { Fail "no Next hint: $($r.Out)" }

# 2: tampered archive aborts, installs nothing.
[IO.File]::AppendAllText((Join-Path $rel $archive), 'junk')
$r = Invoke-Install (Join-Path $T 'bad') @('-NoSetup')
if ($r.Code -eq 0) { Fail 'tampered archive installed' }
if ($r.Out -notmatch 'checksum mismatch') { Fail "no mismatch message: $($r.Out)" }
if (Test-Path (Join-Path (Join-Path $T 'bad') 'oci-assume.exe')) { Fail 'tampered install left a binary' }

# 3: missing checksum entry aborts.
Copy-Item $orig (Join-Path $rel $archive) -Force
Set-Content -Path (Join-Path $rel 'checksums.txt') -Value ''
$r = Invoke-Install (Join-Path $T 'none') @('-NoSetup')
if ($r.Code -eq 0) { Fail 'missing checksum entry installed' }
Write-Sums

# 4: bad version rejected.
$env:OCI_ASSUME_INSTALL_DIR = Join-Path $T 'v'
& $pwsh -NoProfile -File $script -Version v1.2 -NoSetup *> $null
if ($LASTEXITCODE -eq 0) { Fail 'bad version accepted' }

# 5: PATH string logic keeps %VARS% literal and is idempotent.
$raw = '%USERPROFILE%\bin;C:\Tools'
$new = Get-AppendedPath -Raw $raw -Dir 'C:\oa'
if ($new -ne '%USERPROFILE%\bin;C:\Tools;C:\oa') { Fail "append wrong: $new" }
if ((Get-AppendedPath -Raw $new -Dir 'C:\oa\') -ne $new) { Fail 'append not idempotent' }

# 6: registry round-trip on Windows, in a scratch key.
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
