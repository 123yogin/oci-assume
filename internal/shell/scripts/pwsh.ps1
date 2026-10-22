# oci-assume shell hook (pwsh). Add to $PROFILE: oci-assume init pwsh | Out-String | Invoke-Expression
$env:OCI_ASSUME_SHELL = 'pwsh'

function oci-assume {
    $bin = (Get-Command oci-assume -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
    if (-not $bin) {
        [Console]::Error.WriteLine('oci-assume: binary not found on PATH')
        $global:LASTEXITCODE = 1
        return
    }
    $passthrough = @('status','refresh','login','prompt','daemon','init','doctor','setup','uninstall','version','help','completion','__complete','__completeNoDesc','--help','-h','--version')
    if ($args.Count -gt 0 -and $passthrough -contains $args[0]) {
        & $bin @args
        return
    }
    $prev = $env:OCI_ASSUME_EVAL
    $env:OCI_ASSUME_EVAL = '1'
    try {
        $out = & $bin @args
        $rc = $LASTEXITCODE
    } finally {
        $env:OCI_ASSUME_EVAL = $prev
    }
    if ($rc -ne 0) {
        $global:LASTEXITCODE = $rc
        return
    }
    if ($out) {
        Invoke-Expression ($out -join "`n")
    }
}

function OciAssumePrompt {
    $bin = (Get-Command oci-assume -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
    if (-not $bin) { return }
    $p = & $bin prompt 2>$null
    if ($p) { return "[oci $p]" }
}
