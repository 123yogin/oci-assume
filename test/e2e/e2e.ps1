# Fresh-machine end-to-end checks for a published oci-assume release (Windows; PowerShell 5.1 and 7).
# Usage: e2e.ps1 <step>... | all   (steps: prepare install assert-setup setup-rerun shell commands background uninstall)
# Windows profile paths and Task Scheduler are per user and cannot be redirected, so outside CI it needs E2E_ALLOW_REAL_PROFILE=1.
param([Parameter(Mandatory, ValueFromRemainingArguments)][string[]]$Steps)
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$script:Fails = 0
$TaskName = 'oci-assume-refresh'
$MarkBegin = '# >>> oci-assume >>>'
# Kept ASCII: Windows PowerShell 5.1 reads a BOM-less script as ANSI.
$X = [string][char]0x2717

function Ok($m) { Write-Host "ok   $m" }
function Fail($m) { Write-Host "FAIL $m"; $script:Fails++ }
function Skip($m) { Write-Host "skip $m" }
function Check($what, [bool]$cond) { if ($cond) { Ok $what } else { Fail $what } }

if (-not $env:E2E_WORK) { throw 'set E2E_WORK to a scratch directory' }
if ($env:CI -ne 'true' -and $env:E2E_ALLOW_REAL_PROFILE -ne '1') {
    throw 'refusing to run outside CI: setup edits your real PowerShell profiles and Task Scheduler (set E2E_ALLOW_REAL_PROFILE=1 to accept)'
}
$EnvFile = Join-Path $env:E2E_WORK 'env.json'
$Editions = @('pwsh', 'powershell') | Where-Object { Get-Command $_ -ErrorAction SilentlyContinue }
$StateDir = Join-Path $env:APPDATA 'oci-assume'

function Get-UserPath { [Environment]::GetEnvironmentVariable('Path', 'User') }
# What a terminal opened from Explorer sees: machine PATH plus user PATH from the registry.
function Get-FreshPath { [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + (Get-UserPath) }

function Add-UserPathDir($dir) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    try {
        $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if (-not (($raw -split ';') -contains $dir)) {
            $key.SetValue('Path', (@($raw -split ';' | Where-Object { $_ }) + $dir) -join ';', [Microsoft.Win32.RegistryValueKind]::ExpandString)
        }
    } finally { $key.Close() }
}

function Remove-UserPathDir($dir) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    try {
        $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $kept = @($raw -split ';' | Where-Object { $_ -and ($_.TrimEnd('\') -ine $dir.TrimEnd('\')) })
        $key.SetValue('Path', ($kept -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
    } finally { $key.Close() }
}

function Save-Env($h) { $h | ConvertTo-Json | Set-Content -Encoding UTF8 -Path $EnvFile }
function Import-Env {
    if (-not (Test-Path $EnvFile)) { return $null }
    $o = Get-Content -Raw $EnvFile | ConvertFrom-Json
    foreach ($p in $o.PSObject.Properties) {
        if ($p.Name -like 'ENV_*') { Set-Item -Path ("Env:\" + $p.Name.Substring(4)) -Value $p.Value }
    }
    return $o
}

function OA { & (Join-Path $script:Cfg.BinDir 'oci-assume.exe') @args }

function Get-TaskXml {
    $x = schtasks /Query /TN $TaskName /XML 2>$null | Out-String
    $okq = $LASTEXITCODE -eq 0
    $global:LASTEXITCODE = 0
    if ($okq) { $x } else { $null }
}

function Step-Prepare {
    if (-not $env:E2E_VERSION) { throw 'set E2E_VERSION to the tag under test, e.g. v1.1.0' }
    $src = if ($env:E2E_FAKE_SRC) { $env:E2E_FAKE_SRC } else { Join-Path $PSScriptRoot '..\..' }
    $bin = Join-Path $env:E2E_WORK 'bin'
    $snap = Join-Path $env:E2E_WORK 'snap'
    New-Item -ItemType Directory -Force -Path $bin, $snap | Out-Null
    Push-Location $src
    go build -o (Join-Path $bin 'oci.exe') ./internal/testutil/fakeoci/cmd/oci
    $rc = $LASTEXITCODE
    Pop-Location
    if ($rc -ne 0) { throw "build fake oci from $src" }
    Ok "fake oci.exe built from $src"
    # Like the OCI CLI installer: on the user PATH, so new terminals and the task find it.
    Add-UserPathDir $bin
    $env:Path = "$bin;$env:Path"

    $oci = Join-Path $env:USERPROFILE '.oci'
    $s = Join-Path $oci 'sessions\e2e'
    New-Item -ItemType Directory -Force -Path $s | Out-Null
    $pem = "-----BEGIN PRIVATE KEY-----`nZTJlLWZha2U=`n-----END PRIVATE KEY-----`n"
    [IO.File]::WriteAllText((Join-Path $s 'oci_api_key.pem'), $pem)
    [IO.File]::WriteAllText((Join-Path $oci 'e2e-key.pem'), $pem)
    $cfg = @'
[e2e]
fingerprint=12:34:56:78:9a:bc:de:f0:12:34:56:78:9a:bc:de:f0
key_file=~/.oci/sessions/e2e/oci_api_key.pem
tenancy=ocid1.tenancy.oc1..aaaaaaaae2etenancy
region=us-ashburn-1
security_token_file=~/.oci/sessions/e2e/token

[e2e-key]
user=ocid1.user.oc1..aaaaaaaae2euser
fingerprint=fe:dc:ba:98:76:54:32:10:fe:dc:ba:98:76:54:32:10
key_file=~/.oci/e2e-key.pem
tenancy=ocid1.tenancy.oc1..aaaaaaaae2etenancy
region=us-phoenix-1
'@
    [IO.File]::WriteAllText((Join-Path $oci 'config'), $cfg.Replace("`r`n", "`n"))
    $env:FAKE_TOKEN_PATH = Join-Path $s 'token'
    oci session authenticate --profile-name e2e --region us-ashburn-1 | Out-Null
    Check "session token seeded at $env:FAKE_TOKEN_PATH" (Test-Path $env:FAKE_TOKEN_PATH)

    $profiles = @()
    foreach ($ps in $Editions) { $profiles += (& $ps -NoProfile -Command '$PROFILE.CurrentUserAllHosts').Trim() }
    $i = 0
    foreach ($p in $profiles) {
        if (Test-Path $p) { Copy-Item $p (Join-Path $snap "profile$i") } else { New-Item -ItemType File (Join-Path $snap "profile$i.absent") | Out-Null }
        $i++
    }
    Ok "snapshot of $($profiles -join ', ')"
    Save-Env @{
        BinDir = Join-Path $env:LOCALAPPDATA 'Programs\oci-assume'
        Profiles = $profiles
        ENV_Path = $env:Path
        ENV_FAKE_TOKEN_PATH = $env:FAKE_TOKEN_PATH
        ENV_E2E_VERSION = $env:E2E_VERSION
    }
}

function Step-Install {
    $url = "https://raw.githubusercontent.com/123yogin/oci-assume/$($env:E2E_VERSION)/install.ps1"
    Write-Host "+ `$env:OCI_ASSUME_VERSION='$($env:E2E_VERSION)'; `$env:OCI_ASSUME_YES='1'; irm $url | iex"
    $env:OCI_ASSUME_VERSION = $env:E2E_VERSION
    $env:OCI_ASSUME_YES = '1'
    try {
        irm $url | iex
        Ok 'install.ps1 one-liner (it also runs setup --yes; no TTY needed on Windows)'
    } catch {
        Fail "install.ps1 one-liner: $_"
    }
    Check "binary at $($script:Cfg.BinDir)\oci-assume.exe" (Test-Path (Join-Path $script:Cfg.BinDir 'oci-assume.exe'))
    Check 'windowless helper oci-assume-bg.exe installed' (Test-Path (Join-Path $script:Cfg.BinDir 'oci-assume-bg.exe'))
}

function Step-AssertSetup {
    foreach ($p in $script:Cfg.Profiles) {
        $t = [string]$(if (Test-Path $p) { Get-Content -Raw $p })
        Check "marked block in $p" ($t.Contains($MarkBegin))
        Check "hook line in $p" ($t.Contains('oci-assume init pwsh'))
    }
    $dirs = (Get-UserPath) -split ';' | ForEach-Object { $_.TrimEnd('\') }
    Check "user PATH has $($script:Cfg.BinDir) (installer; no profile PATH line needed)" ($dirs -contains $script:Cfg.BinDir.TrimEnd('\'))
    Check "receipt at $StateDir\receipt.json" (Test-Path (Join-Path $StateDir 'receipt.json'))
    $xml = Get-TaskXml
    Check "schtasks \$TaskName registered" ([bool]$xml)
    Check 'task has a logon trigger' ($xml -match '<LogonTrigger>')
    Check 'task runs oci-assume-bg.exe' ($xml -match [regex]::Escape('oci-assume-bg.exe'))
    foreach ($ps in $Editions) {
        $eff = (& $ps -NoProfile -Command 'Get-ExecutionPolicy').Trim()
        $cu = (& $ps -NoProfile -Command 'Get-ExecutionPolicy -Scope CurrentUser').Trim()
        Check "${ps}: execution policy $eff (CurrentUser: $cu) allows the profile" ($eff -in 'RemoteSigned', 'Unrestricted', 'Bypass')
    }
}

function Step-SetupRerun {
    $out = OA setup --yes --no-oci-cli --no-login | Out-String
    $rc = $LASTEXITCODE
    Write-Host $out
    Check 'setup re-run exits 0' ($rc -eq 0)
    Check 'setup re-run: shell-hook already set up' ($out -match 'shell-hook already set up')
    Check 'setup re-run: background already set up' ($out -match 'background already set up')
}

# Lines doctor may mark with an x that are not product failures.
function Get-DoctorAllowed([bool]$hooked) {
    $a = @()
    if (-not $hooked) { $a += "^$X shell hook not loaded" }
    & oci --version *> $null
    if ($LASTEXITCODE -ne 0) { $a += "^$X oci CLI" }
    $global:LASTEXITCODE = 0
    return $a
}

function Test-DoctorMarks($lines, $allowed) {
    $good = $true
    foreach ($l in $lines) {
        if (-not $l.StartsWith($X)) { continue }
        $hit = $false
        foreach ($re in $allowed) { if ($l -match $re) { $hit = $true } }
        if ($hit) { Write-Host "     expected: $l" } else { Write-Host "     unexpected: $l"; $good = $false }
    }
    return $good
}

$Probe = @'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
function U($v) { if ($null -eq $v) { '<unset>' } else { $v } }
"FN=" + [bool](Get-Command oci-assume -CommandType Function -ErrorAction SilentlyContinue)
oci-assume e2e; "RC1=$LASTEXITCODE"
"P1=$env:OCI_CLI_PROFILE"; "A1=$(U $env:OCI_CLI_AUTH)"; "R1=$env:OCI_CLI_REGION"
"PROMPT=$(OciAssumePrompt)"
oci-assume doctor | Out-File -Encoding utf8 $env:E2E_DOCTOR_OUT; "DOC=$LASTEXITCODE"
oci-assume e2e-key; "RC2=$LASTEXITCODE"
"P2=$env:OCI_CLI_PROFILE"; "A2=$(U $env:OCI_CLI_AUTH)"
'@

# Starts a new shell that loads its profile, with the PATH a new terminal would get.
function Invoke-NewShell($ps, $script) {
    $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($script))
    $saved = $env:Path
    $env:Path = Get-FreshPath
    try { & $ps -NoLogo -EncodedCommand $enc 2>&1 | ForEach-Object { "$_" } } finally { $env:Path = $saved }
}

function Step-Shell {
    foreach ($ps in $Editions) {
        $env:E2E_DOCTOR_OUT = Join-Path $env:E2E_WORK "doctor-$ps.txt"
        $out = @(Invoke-NewShell $ps $Probe)
        Write-Host "--- $ps (new shell, profile loaded)"; $out | ForEach-Object { Write-Host "     $_" }
        function V($k) { ($out | Where-Object { $_ -like "$k=*" } | Select-Object -Last 1) -replace "^$k=", '' }
        Check "${ps}: oci-assume is a shell function" ((V 'FN') -eq 'True')
        Check "${ps}: oci-assume e2e exits 0" ((V 'RC1') -eq '0')
        Check "${ps}: OCI_CLI_PROFILE=e2e" ((V 'P1') -eq 'e2e')
        Check "${ps}: OCI_CLI_AUTH=security_token" ((V 'A1') -eq 'security_token')
        Check "${ps}: OCI_CLI_REGION=us-ashburn-1" ((V 'R1') -eq 'us-ashburn-1')
        Check "${ps}: prompt helper prints $(V 'PROMPT')" ((V 'PROMPT') -match '^\[oci e2e \d+m\]$')
        Check "${ps}: oci-assume e2e-key switches profile" ((V 'RC2') -eq '0' -and (V 'P2') -eq 'e2e-key')
        Check "${ps}: e2e-key clears OCI_CLI_AUTH" ((V 'RC2') -eq '0' -and (V 'A2') -eq '<unset>')
        $doc = @(if (Test-Path $env:E2E_DOCTOR_OUT) { Get-Content -Encoding UTF8 $env:E2E_DOCTOR_OUT })
        Write-Host "--- doctor in hooked $ps"; $doc | ForEach-Object { Write-Host "     $_" }
        Check "${ps}: doctor ran" ($doc.Count -gt 0)
        Check "${ps}: doctor in a hooked shell has no unexpected x-marks" (Test-DoctorMarks $doc (Get-DoctorAllowed $true))
    }
}

function Get-E2EExpiry {
    $rows = OA status --json | Out-String | ConvertFrom-Json
    $r = $rows | Where-Object { $_.profile -eq 'e2e' }
    if ($r -and $r.expires) { [datetime]$r.expires } else { $null }
}

function Step-Commands {
    $out = OA status | Out-String
    Check 'oci-assume status exits 0' ($LASTEXITCODE -eq 0)
    Write-Host $out
    Check 'status lists e2e with its expiry' ($out -match '(?m)^e2e +us-ashburn-1 +\d+m')
    Check 'status lists e2e-key' ($out -match '(?m)^e2e-key +us-phoenix-1')

    $doc = @(OA doctor)
    Write-Host '--- doctor (no hook in this shell)'; $doc | ForEach-Object { Write-Host "     $_" }
    Check "doctor without a hook reports 'shell hook not loaded'" (@($doc | Where-Object { $_ -like "$X shell hook not loaded*" }).Count -eq 1)
    Check 'doctor has no unexpected x-marks' (Test-DoctorMarks $doc (Get-DoctorAllowed $false))

    $before = Get-E2EExpiry
    Start-Sleep -Seconds 2
    $r = OA refresh e2e --threshold 24h | Out-String
    Check "refresh e2e: $($r.Trim())" ($LASTEXITCODE -eq 0)
    $after = Get-E2EExpiry
    Check "token expiry moved $before -> $after" ($before -and $after -and $after -gt $before)

    $v = (OA version | Out-String).Trim()
    $tag = $env:E2E_VERSION
    Check "version: $v" ($v.StartsWith("oci-assume $($tag.Substring(1)) ") -or $v.StartsWith("oci-assume $tag "))
}

function Test-BackgroundResult {
    $f = Join-Path $StateDir 'state.json'
    if (-not (Test-Path $f)) { return $false }
    $s = Get-Content -Raw $f | ConvertFrom-Json
    $r = $s.last_result.e2e
    return [bool]($r -and $r.background -and $r.ok)
}

function Step-Background {
    schtasks /Run /TN $TaskName | Out-Null
    Check "schtasks /Run /TN $TaskName" ($LASTEXITCODE -eq 0)
    $i = 0
    while ($i -lt 60 -and -not (Test-BackgroundResult)) { Start-Sleep -Seconds 1; $i++ }
    if (Test-BackgroundResult) { Ok "state.json has a background result for e2e after ${i}s" } else {
        Fail 'no background result for e2e in 60s'
        Get-Content -Raw (Join-Path $StateDir 'state.json') -ErrorAction SilentlyContinue | Write-Host
        schtasks /Query /TN $TaskName /V /FO LIST | Write-Host
    }
}

function Step-Uninstall {
    $out = OA uninstall | Out-String
    Write-Host $out
    Check 'oci-assume uninstall' ($LASTEXITCODE -eq 0)
    $snap = Join-Path $env:E2E_WORK 'snap'
    $i = 0
    foreach ($p in $script:Cfg.Profiles) {
        if (Test-Path (Join-Path $snap "profile$i.absent")) {
            Check "$p absent again" (-not (Test-Path $p))
        } else {
            $same = (Test-Path $p) -and ((Get-FileHash $p).Hash -eq (Get-FileHash (Join-Path $snap "profile$i")).Hash)
            Check "$p byte-identical to the snapshot" $same
        }
        $i++
    }
    Check "task \$TaskName gone" (-not (Get-TaskXml))
    Check 'receipt gone' (-not (Test-Path (Join-Path $StateDir 'receipt.json')))

    # The documented manual removal for install.ps1 users.
    $dir = $script:Cfg.BinDir
    Start-Sleep -Seconds 2
    Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
    Remove-UserPathDir $dir
    Check "removed $dir" (-not (Test-Path $dir))
    $dirs = (Get-UserPath) -split ';' | ForEach-Object { $_.TrimEnd('\') }
    Check 'install dir gone from user PATH' (-not ($dirs -contains $dir.TrimEnd('\')))
    foreach ($ps in $Editions) {
        $o = @(Invoke-NewShell $ps '"FOUND=" + [bool](Get-Command oci-assume -ErrorAction SilentlyContinue)')
        Check "${ps}: new shell no longer finds oci-assume" ($o -contains 'FOUND=False')
    }
}

$script:Cfg = Import-Env
foreach ($s in $Steps) {
    if ($s -ne 'prepare' -and $s -ne 'all' -and -not $script:Cfg) { throw "run the prepare step first ($EnvFile missing)" }
    $all = if ($s -eq 'all') { 'prepare', 'install', 'assert-setup', 'setup-rerun', 'shell', 'commands', 'background', 'uninstall' } else { @($s) }
    foreach ($t in $all) {
        Write-Host "=== $t"
        switch ($t) {
            'prepare' { Step-Prepare; $script:Cfg = Import-Env }
            'install' { Step-Install }
            'assert-setup' { Step-AssertSetup }
            'setup-rerun' { Step-SetupRerun }
            'shell' { Step-Shell }
            'commands' { Step-Commands }
            'background' { Step-Background }
            'uninstall' { Step-Uninstall }
            default { throw "unknown step: $t" }
        }
    }
}
if ($script:Fails -gt 0) { Write-Host "$($script:Fails) check(s) failed"; exit 1 }
exit 0
