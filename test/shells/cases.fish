#!/usr/bin/env fish
# Shell integration cases for fish. Env fixtures come from inside.sh.
set -g fails 0
function check
    if test "$argv[2]" = "$argv[3]"
        echo "ok   $argv[1]"
    else
        echo "FAIL $argv[1]: want [$argv[2]] got [$argv[3]]"
        set -g fails (math $fails + 1)
    end
end
function val
    if set -q $argv[1]
        echo -n $$argv[1]
    else
        echo -n unset
    end
end
oci-assume init fish | source
set H "it's \$x"

# a
set -gx FAKE_OCI_MODE expired
oci-assume dev </dev/null 2>/dev/null
check "a rc" 2 $status
check "a env unset" unset (val OCI_CLI_PROFILE)

# b
set -gx FAKE_OCI_MODE ok
sh ./seed.sh dev
oci-assume dev </dev/null 2>/dev/null
check "b rc" 0 $status
check "b profile" dev "$OCI_CLI_PROFILE"
check "b auth" security_token "$OCI_CLI_AUTH"
check "b region" us-ashburn-1 "$OCI_CLI_REGION"
set p (oci_assume_prompt)
if test "$p" = "[oci dev 59m]"; or test "$p" = "[oci dev 60m]"
    echo "ok   b prompt"
else
    echo "FAIL b prompt: [$p]"
    set fails (math $fails + 1)
end

# b2
sh ./seed.sh norgn
oci-assume norgn </dev/null 2>/dev/null
check "b2 rc" 0 $status
check "b2 region unset" unset (val OCI_CLI_REGION)

# c
oci-assume apikey </dev/null 2>/dev/null
check "c rc" 0 $status
check "c profile" apikey "$OCI_CLI_PROFILE"
check "c auth unset" unset (val OCI_CLI_AUTH)

# d
oci-assume dev </dev/null 2>/dev/null
oci-assume --unset </dev/null 2>/dev/null
check "d rc" 0 $status
oci-assume --unset </dev/null 2>/dev/null
check "d rc2" 0 $status
check "d vars" "unset unset unset" (val OCI_CLI_PROFILE)" "(val OCI_CLI_AUTH)" "(val OCI_CLI_REGION)

# e
sh ./seed.sh hostile
oci-assume -- "$H" </dev/null 2>/dev/null
check "e rc" 0 $status
check "e roundtrip" "$H" "$OCI_CLI_PROFILE"
oci-assume --unset </dev/null 2>/dev/null

# f
oci-assume dev --help </dev/null >/dev/null 2>&1
check "f rc" 0 $status
check "f env" unset (val OCI_CLI_PROFILE)

# g
set out (oci-assume status </dev/null 2>/dev/null)
if string match -q "*PROFILE*" -- "$out"
    echo "ok   g stdout"
else
    echo "FAIL g: [$out]"
    set fails (math $fails + 1)
end
check "g env" unset (val OCI_CLI_PROFILE)

# h
set out (oci-assume __complete "" </dev/null 2>/dev/null | string collect)
if string match -q "*status*" -- "$out"
    echo "ok   h candidates"
else
    echo "FAIL h: [$out]"
    set fails (math $fails + 1)
end

test $fails -eq 0
