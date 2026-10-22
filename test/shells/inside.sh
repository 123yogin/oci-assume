#!/bin/sh
# Runs in the container: builds fixtures, then execs the shell's case script.
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
case "$SHELLNAME" in
  zsh) exec zsh -f ./cases.zsh ;;
  bash) exec bash --noprofile --norc ./cases.bash ;;
  fish) exec fish --no-config ./cases.fish ;;
  pwsh) exec pwsh -NoProfile -NonInteractive -File ./cases.ps1 ;;
esac
