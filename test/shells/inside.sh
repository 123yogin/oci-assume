#!/bin/sh
# Runs in the container: builds fixtures, runs the shell's case script, then the setup/uninstall case.
set -eu
T=$(mktemp -d)
mkdir -p "$T/state"
cp /src/test/shells/.bin/oci-assume /usr/local/bin/oci-assume
cp /src/internal/testutil/fakeoci/oci "$T/oci"
chmod 755 "$T/oci"
cat > "$T/config" <<'CFG'
[dev]
region=us-ashburn-1
security_token_file=@T@/dev.token
key_file=@T@/key.pem

[apikey]
region=us-phoenix-1
key_file=@T@/key.pem

[norgn]
security_token_file=@T@/norgn.token
key_file=@T@/key.pem

[it's $x]
region=us-ashburn-1
security_token_file=@T@/hostile.token
key_file=@T@/key.pem
CFG
sed -i "s|@T@|$T|g" "$T/config"
printf 'not-a-key\n' > "$T/key.pem"
chmod 600 "$T/key.pem"
export OCI_CLI_CONFIG_FILE="$T/config" OCI_ASSUME_STATE_DIR="$T/state" \
  OCI_ASSUME_OCI_BIN="$T/oci" TESTDIR="$T" HOME="$T"
unset OCI_CLI_PROFILE OCI_CLI_AUTH OCI_CLI_REGION
cd /src/test/shells
rc=0
case "$SHELLNAME" in
  zsh) zsh -f ./cases.zsh || rc=1 ;;
  bash) bash --noprofile --norc ./cases.bash || rc=1 ;;
  fish) fish --no-config ./cases.fish || rc=1 ;;
  pwsh) pwsh -NoProfile -NonInteractive -File ./cases.ps1 || rc=1 ;;
esac
sh ./setup.sh "$SHELLNAME" || rc=1
exit $rc
