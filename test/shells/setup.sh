#!/bin/sh
# Usage: setup.sh zsh|bash|fish|pwsh. setup adds the hook for a fresh shell; uninstall restores the rc byte for byte.
set -u
sh=$1
fails=0
pass() { echo "ok   setup $1"; }
fail() { echo "FAIL setup $1"; fails=$((fails + 1)); }

H=$(mktemp -d)
export HOME="$H" OCI_ASSUME_STATE_DIR="$H/state" SHELL="$(command -v "$sh")"
unset ZDOTDIR XDG_CONFIG_HOME CI
case "$sh" in
  zsh) rc="$H/.zshrc" ;;
  bash) rc="$H/.bashrc" ;;
  fish) rc="$H/.config/fish/config.fish" ;;
  pwsh) rc="$H/.config/powershell/profile.ps1" ;;
esac
mkdir -p "$(dirname "$rc")"
# No trailing newline: setup must not leave one behind after uninstall.
printf '# user rc\nexport KEEP=1' > "$rc"
cp "$rc" "$H/rc.orig"

# has_hook prints "yes" when a fresh interactive shell defines the function and the prompt hook.
has_hook() {
  case "$sh" in
    zsh) zsh -i -c 'whence -w oci-assume oci_assume_prompt' 2>/dev/null | grep -c function ;;
    bash) bash -i -c 'type -t oci-assume oci_assume_prompt' 2>/dev/null | grep -c function ;;
    fish) fish -i -c 'functions -q oci-assume; and echo function; functions -q oci_assume_prompt; and echo function' 2>/dev/null | grep -c function ;;
    pwsh) pwsh -NonInteractive -Command '(Get-Command oci-assume,OciAssumePrompt -CommandType Function -ErrorAction SilentlyContinue).Count' 2>/dev/null | tr -d '\r' ;;
  esac
}

out=$(oci-assume setup --yes --no-oci-cli --no-login </dev/null 2>&1) && pass "rc" || { fail "rc: $out"; }
case "$out" in *"shell-hook done"*) pass "hook applied" ;; *) fail "hook applied: $out" ;; esac
n=$(has_hook)
[ "$n" = 2 ] && pass "fresh shell has hook" || fail "fresh shell has hook: [$n]"
crontab -l 2>/dev/null | grep -q oci-assume && pass "background installed" || fail "background installed"

out=$(oci-assume setup --yes --no-oci-cli --no-login </dev/null 2>&1)
case "$out" in *"shell-hook already set up"*) pass "rerun no-op" ;; *) fail "rerun no-op: $out" ;; esac

out=$(oci-assume uninstall </dev/null 2>&1) && pass "uninstall rc" || fail "uninstall rc: $out"
n=$(has_hook)
[ "$n" = 0 ] && pass "hook gone" || fail "hook gone: [$n]"
cmp -s "$rc" "$H/rc.orig" && pass "rc byte-identical" || fail "rc byte-identical: $(od -c "$rc" | head -5)"
[ ! -e "$H/.config/fish/conf.d/oci-assume.fish" ] && pass "no drop-in" || fail "no drop-in"
crontab -l 2>/dev/null | grep -q oci-assume && fail "background removed" || pass "background removed"
[ ! -e "$OCI_ASSUME_STATE_DIR/receipt.json" ] && pass "receipt removed" || fail "receipt removed"

exit $((fails > 0))
