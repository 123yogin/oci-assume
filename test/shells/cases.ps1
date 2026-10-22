# Shell integration cases for pwsh. Env fixtures come from inside.sh.
$script:fails = 0
function Check($name, $want, $got) {
    if ($want -ceq $got) { Write-Host "ok   $name" }
    else { Write-Host "FAIL ${name}: want [$want] got [$got]"; $script:fails++ }
}
function Val($n) { $v = [Environment]::GetEnvironmentVariable($n); if ($null -eq $v) { 'unset' } else { $v } }
oci-assume init pwsh | Out-String | Invoke-Expression
$H = 'it''s $x'

# a
$env:FAKE_OCI_MODE = 'expired'
$LASTEXITCODE = 99; oci-assume dev 2>$null
Check 'a rc' 2 $LASTEXITCODE
Check 'a env unset' 'unset' (Val 'OCI_CLI_PROFILE')

# b
$env:FAKE_OCI_MODE = 'ok'
sh ./seed.sh dev
$LASTEXITCODE = 99; oci-assume dev 2>$null
Check 'b rc' 0 $LASTEXITCODE
Check 'b profile' 'dev' $env:OCI_CLI_PROFILE
Check 'b auth' 'security_token' $env:OCI_CLI_AUTH
Check 'b region' 'us-ashburn-1' $env:OCI_CLI_REGION
$p = OciAssumePrompt
if ($p -ceq '[oci dev 59m]' -or $p -ceq '[oci dev 60m]') { Write-Host 'ok   b prompt' }
else { Write-Host "FAIL b prompt: [$p]"; $script:fails++ }

# b2
sh ./seed.sh norgn
$LASTEXITCODE = 99; oci-assume norgn 2>$null
Check 'b2 rc' 0 $LASTEXITCODE
Check 'b2 region unset' 'unset' (Val 'OCI_CLI_REGION')

# c
$LASTEXITCODE = 99; oci-assume apikey 2>$null
Check 'c rc' 0 $LASTEXITCODE
Check 'c profile' 'apikey' $env:OCI_CLI_PROFILE
Check 'c auth unset' 'unset' (Val 'OCI_CLI_AUTH')

# d
$LASTEXITCODE = 99; oci-assume dev 2>$null
$LASTEXITCODE = 99; oci-assume --unset 2>$null
Check 'd rc' 0 $LASTEXITCODE
$LASTEXITCODE = 99; oci-assume --unset 2>$null
Check 'd rc2' 0 $LASTEXITCODE
Check 'd vars' 'unset unset unset' "$(Val 'OCI_CLI_PROFILE') $(Val 'OCI_CLI_AUTH') $(Val 'OCI_CLI_REGION')"

# e
sh ./seed.sh hostile
$LASTEXITCODE = 99; oci-assume -- $H 2>$null
Check 'e rc' 0 $LASTEXITCODE
Check 'e roundtrip' $H $env:OCI_CLI_PROFILE
$LASTEXITCODE = 99; oci-assume --unset 2>$null

# f
$LASTEXITCODE = 99; oci-assume dev --help *>$null
Check 'f rc' 0 $LASTEXITCODE
Check 'f env' 'unset' (Val 'OCI_CLI_PROFILE')

# g
$LASTEXITCODE = 99; $out = oci-assume status 2>$null
if ($out -match 'PROFILE') { Write-Host 'ok   g stdout' } else { Write-Host "FAIL g: [$out]"; $script:fails++ }
Check 'g env' 'unset' (Val 'OCI_CLI_PROFILE')

# h
$out = (oci-assume __complete '' 2>$null) -join "`n"
if ($out -match 'status') { Write-Host 'ok   h candidates' } else { Write-Host "FAIL h: [$out]"; $script:fails++ }

exit ([int]($script:fails -gt 0))
