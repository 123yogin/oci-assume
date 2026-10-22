#!/bin/zsh
# Shell integration cases for zsh. Env fixtures come from inside.sh.
fails=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: want [$2] got [$3]"; fails=$((fails+1)); fi
}
eval "$(oci-assume init zsh)"
H="it's \$x"

# a. expired, non-interactive
export FAKE_OCI_MODE=expired
oci-assume dev </dev/null 2>/dev/null; check "a rc" 2 $?
check "a env unset" "" "${OCI_CLI_PROFILE-}"

# b. ok with fresh token
export FAKE_OCI_MODE=ok
sh ./seed.sh dev
oci-assume dev </dev/null 2>/dev/null; check "b rc" 0 $?
check "b profile" dev "$OCI_CLI_PROFILE"
check "b auth" security_token "$OCI_CLI_AUTH"
check "b region" us-ashburn-1 "$OCI_CLI_REGION"
p=$(oci_assume_prompt)
if [ "$p" = "[oci dev 59m]" ] || [ "$p" = "[oci dev 60m]" ]; then echo "ok   b prompt"; else echo "FAIL b prompt: [$p]"; fails=$((fails+1)); fi

# b2. region-less session profile
sh ./seed.sh norgn
oci-assume norgn </dev/null 2>/dev/null; check "b2 rc" 0 $?
check "b2 region unset" "unset" "${OCI_CLI_REGION-unset}"

# c. api-key profile
oci-assume apikey </dev/null 2>/dev/null; check "c rc" 0 $?
check "c profile" apikey "$OCI_CLI_PROFILE"
check "c auth unset" "unset" "${OCI_CLI_AUTH-unset}"

# d. unset
oci-assume dev </dev/null 2>/dev/null
oci-assume --unset </dev/null 2>/dev/null; check "d rc" 0 $?
oci-assume --unset </dev/null 2>/dev/null; check "d rc2" 0 $?
check "d vars" "unset unset unset" "${OCI_CLI_PROFILE-unset} ${OCI_CLI_AUTH-unset} ${OCI_CLI_REGION-unset}"

# e. hostile profile
sh ./seed.sh hostile
oci-assume -- "$H" </dev/null 2>/dev/null; check "e rc" 0 $?
check "e roundtrip" "$H" "$OCI_CLI_PROFILE"
oci-assume --unset </dev/null 2>/dev/null

# f. --help leaves env alone
oci-assume dev --help </dev/null >/dev/null 2>&1; check "f rc" 0 $?
check "f env" "unset" "${OCI_CLI_PROFILE-unset}"

# g. status passthrough
out=$(oci-assume status </dev/null 2>/dev/null)
case "$out" in *PROFILE*) echo "ok   g stdout" ;; *) echo "FAIL g: [$out]"; fails=$((fails+1)) ;; esac
check "g env" "unset" "${OCI_CLI_PROFILE-unset}"

# h. completion passthrough
out=$(oci-assume __complete "" </dev/null 2>/dev/null)
case "$out" in *status*) echo "ok   h candidates" ;; *) echo "FAIL h: [$out]"; fails=$((fails+1)) ;; esac

exit $((fails > 0))
