# Installs oci-assume from a GitHub release after verifying its checksum.
# Usage: iwr -useb https://raw.githubusercontent.com/123yogin/oci-assume/main/install.ps1 | iex
# Env: OCI_ASSUME_VERSION, OCI_ASSUME_NO_SETUP, OCI_ASSUME_BASE_URL (test override),
#      OCI_ASSUME_INSTALL_DIR (test override of the install dir)
param([string]$Version, [switch]$NoSetup, [switch]$Yes)

$script:Repo = '123yogin/oci-assume'

# Appends $Dir to a raw (unexpanded) PATH string unless already present.
function Get-AppendedPath {
    param([string]$Raw, [string]$Dir)
    $parts = @($Raw -split ';' | Where-Object { $_ -ne '' })
    foreach ($p in $parts) {
        if ($p.TrimEnd('\') -ieq $Dir.TrimEnd('\')) { return $Raw }
    }
    return ((@($parts) + $Dir) -join ';')
}

# Writes the user PATH as REG_EXPAND_SZ without expanding %VARS% already in it.
function Set-UserPathDir {
    param([string]$Dir, [string]$SubKey = 'Environment')
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($SubKey)
    try {
        $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $new = Get-AppendedPath -Raw $raw -Dir $Dir
        if ($new -ne $raw) {
            $key.SetValue('Path', $new, [Microsoft.Win32.RegistryValueKind]::ExpandString)
        }
    } finally { $key.Close() }
}

function Send-EnvironmentChange {
    if (-not ('OciAssume.NativeMethods' -as [type])) {
        Add-Type -Namespace OciAssume -Name NativeMethods -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true, CharSet = System.Runtime.InteropServices.CharSet.Auto)]
public static extern System.IntPtr SendMessageTimeout(System.IntPtr hWnd, uint Msg, System.UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out System.UIntPtr lpdwResult);
'@
    }
    $r = [UIntPtr]::Zero
    # HWND_BROADCAST, WM_SETTINGCHANGE, SMTO_ABORTIFHUNG
    [void][OciAssume.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$r)
}

function Test-IsWindows {
    return ($PSVersionTable.PSVersion.Major -lt 6) -or [bool]$IsWindows
}

function Install-OciAssume {
    [CmdletBinding()]
    param([string]$Version, [switch]$NoSetup, [switch]$Yes)
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

    if (-not $Version) { $Version = $env:OCI_ASSUME_VERSION }
    if (-not $NoSetup -and $env:OCI_ASSUME_NO_SETUP) { $NoSetup = [switch]$true }

    $archId = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } elseif ($env:PROCESSOR_ARCHITECTURE) { $env:PROCESSOR_ARCHITECTURE } else { [string][Runtime.InteropServices.RuntimeInformation]::OSArchitecture }
    switch -Regex ($archId) {
        '^(AMD64|X64)$' { $arch = 'amd64' }
        '^ARM64$' { $arch = 'arm64' }
        default { throw "unsupported architecture: $archId" }
    }

    if (-not $Version) {
        $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$($script:Repo)/releases/latest" -Headers @{ 'User-Agent' = 'oci-assume-install' }
        $Version = $rel.tag_name
        if (-not $Version) { throw 'could not resolve the latest release' }
    }
    if ($Version -notmatch '^v') { $Version = "v$Version" }
    if ($Version -notmatch '^v\d+\.\d+\.\d+$') { throw "invalid version: $Version (want vX.Y.Z)" }

    # Must match name_template in .goreleaser.yaml (Version has no leading v).
    $archive = "oci-assume_$($Version.Substring(1))_windows_$arch.zip"
    $base = if ($env:OCI_ASSUME_BASE_URL) { $env:OCI_ASSUME_BASE_URL } else { "https://github.com/$($script:Repo)/releases/download/$Version" }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("oci-assume-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $wc = New-Object Net.WebClient
        try {
            Write-Host "Downloading $archive ($Version)"
            $wc.DownloadFile("$base/$archive", (Join-Path $tmp $archive))
            $wc.DownloadFile("$base/checksums.txt", (Join-Path $tmp 'checksums.txt'))
        } finally { $wc.Dispose() }

        $want = $null
        foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
            $f = $line.Trim() -split '\s+'
            if ($f.Count -ge 2 -and ($f[1] -eq $archive -or $f[1] -eq "*$archive")) { $want = $f[0]; break }
        }
        if (-not $want) { throw "no checksum entry for $archive; aborting" }
        $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $archive)).Hash
        if ($got -ine $want) { throw "checksum mismatch for $archive (want $want, got $got); aborting" }

        $x = Join-Path $tmp 'x'
        Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $x -Force
        if (-not (Test-Path (Join-Path $x 'oci-assume.exe'))) { throw 'archive has no oci-assume.exe' }

        $dest = if ($env:OCI_ASSUME_INSTALL_DIR) { $env:OCI_ASSUME_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\oci-assume' }
        New-Item -ItemType Directory -Force -Path $dest | Out-Null
        foreach ($exe in 'oci-assume.exe', 'oci-assume-bg.exe') {
            $src = Join-Path $x $exe
            if (Test-Path $src) { Copy-Item -Force $src (Join-Path $dest $exe) }
        }
        Write-Host "Installed oci-assume $Version to $dest"
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }

    if (Test-IsWindows -and -not $env:OCI_ASSUME_INSTALL_DIR) {
        Set-UserPathDir -Dir $dest
        Send-EnvironmentChange
    }
    if (-not (($env:Path -split ';') -contains $dest)) { $env:Path = "$env:Path;$dest" }

    if ($NoSetup) {
        Write-Host 'Next: oci-assume setup'
    } else {
        $setupArgs = @('setup')
        if ($Yes) { $setupArgs += '--yes' }
        & (Join-Path $dest 'oci-assume.exe') @setupArgs
    }
}

$ErrorActionPreference = 'Stop'
Install-OciAssume -Version $Version -NoSetup:$NoSetup -Yes:$Yes
