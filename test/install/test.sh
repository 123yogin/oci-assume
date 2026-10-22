#!/bin/sh
# Serves a fake release via file:// and checks install.sh's checksum handling.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
trap 'exit 130' INT TERM

case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "unsupported OS"; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "unsupported arch"; exit 1 ;; esac
archive="oci-assume_9.9.9_${os}_${arch}.tar.gz"

sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi; }
fail() { echo "FAIL: $*" >&2; exit 1; }

mkdir -p "$T/rel" "$T/pkg"
# shellcheck disable=SC2016
printf '#!/bin/sh\nif [ "${1:-}" = setup ]; then echo "setup $* inst=${OCI_ASSUME_INSTALLER:-}" >> "$SETUP_LOG"; exit "${FAKE_SETUP_EXIT:-0}"; fi\necho fake-oci-assume\n' > "$T/pkg/oci-assume"
chmod 755 "$T/pkg/oci-assume"
tar -czf "$T/rel/$archive" -C "$T/pkg" oci-assume
(cd "$T/rel" && sha "$archive" | awk '{ print $1 "  " $2 }' > checksums.txt)

SETUP_LOG="$T/setup.log"; export SETUP_LOG
run() { OCI_ASSUME_BASE_URL="file://$T/rel" OCI_ASSUME_INSTALL_DIR="$1" sh "$root/install.sh" --version v9.9.9 >/dev/null 2>"$T/err"; }

# 1: correct checksum installs.
run "$T/ok" || fail "valid release did not install"
[ -x "$T/ok/oci-assume" ] || fail "binary missing after valid install"
[ "$("$T/ok/oci-assume")" = fake-oci-assume ] || fail "installed binary wrong"

# 2: tampered archive aborts, installs nothing.
cp "$T/rel/$archive" "$T/orig.tgz"
printf 'junk' >> "$T/rel/$archive"
if run "$T/bad"; then fail "tampered archive installed"; fi
grep -q 'checksum mismatch' "$T/err" || fail "no mismatch message"
[ ! -e "$T/bad/oci-assume" ] || fail "tampered install left a binary"

# 3: missing checksum entry aborts, installs nothing.
cp "$T/orig.tgz" "$T/rel/$archive"
: > "$T/rel/checksums.txt"
if run "$T/missing"; then fail "missing checksum entry installed"; fi
[ ! -e "$T/missing/oci-assume" ] || fail "missing-entry install left a binary"

# 4: missing archive fails cleanly.
rm "$T/rel/$archive"
(cd "$T/rel" && printf 'deadbeef  %s\n' "$archive" > checksums.txt)
if run "$T/gone"; then fail "missing archive installed"; fi
[ ! -e "$T/gone/oci-assume" ] || fail "missing-archive install left a binary"

# 5: non-gzip archive with a matching checksum fails cleanly.
printf 'not a gzip file' > "$T/rel/$archive"
(cd "$T/rel" && sha "$archive" | awk '{ print $1 "  " $2 }' > checksums.txt)
if run "$T/nogz"; then fail "non-gzip archive installed"; fi
[ ! -e "$T/nogz/oci-assume" ] || fail "non-gzip install left a binary"

# 6: bad --version values are rejected.
for v in v1.2 ../x v1/2.3.4; do
  if OCI_ASSUME_BASE_URL="file://$T/rel" OCI_ASSUME_INSTALL_DIR="$T/v" sh "$root/install.sh" --version "$v" >/dev/null 2>&1; then
    fail "version $v accepted"
  fi
done

# 7: setup handoff. No tty: prints Next hint, never runs setup.
cp "$T/orig.tgz" "$T/rel/$archive"
(cd "$T/rel" && sha "$archive" | awk '{ print $1 "  " $2 }' > checksums.txt)
: > "$SETUP_LOG"
run "$T/s1" || fail "install failed"
grep -q 'Next: oci-assume setup' "$T/err" || fail "no Next hint without tty"
[ ! -s "$SETUP_LOG" ] || fail "setup ran without a tty"

# 8: pty runs setup; --yes passes through; --no-setup and env suppress it.
pty() {
  extra=${3:-}
  cmd="$extra OCI_ASSUME_BASE_URL=file://$T/rel OCI_ASSUME_INSTALL_DIR=$1 SETUP_LOG=$SETUP_LOG sh $root/install.sh --version v9.9.9 $2"
  if [ "$(uname -s)" = Darwin ]; then
    script -q /dev/null sh -c "$cmd" >"$T/pty.out" 2>&1 </dev/null && PTY_RC=0 || PTY_RC=$?
  else
    script -qec "$cmd" /dev/null >"$T/pty.out" 2>&1 </dev/null && PTY_RC=0 || PTY_RC=$?
  fi
}
if command -v script >/dev/null 2>&1; then
  : > "$SETUP_LOG"; pty "$T/p1" ""
  grep -qx 'setup setup inst=1' "$SETUP_LOG" || fail "setup not run on pty: $(cat "$SETUP_LOG")"
  # The PATH hint is repeated after setup's output, so it is the last thing the user sees.
  [ "$(grep -c "Add $T/p1 to your PATH" "$T/pty.out")" = 2 ] || fail "PATH hint not repeated after setup: $(cat "$T/pty.out")"
  : > "$SETUP_LOG"; pty "$T/p2" "--yes"
  grep -qx 'setup setup --yes inst=1' "$SETUP_LOG" || fail "--yes not passed: $(cat "$SETUP_LOG")"
  : > "$SETUP_LOG"; pty "$T/p3" "--no-setup"
  [ ! -s "$SETUP_LOG" ] || fail "--no-setup ignored"
  : > "$SETUP_LOG"; pty "$T/p4" "" OCI_ASSUME_NO_SETUP=1
  [ ! -s "$SETUP_LOG" ] || fail "OCI_ASSUME_NO_SETUP ignored"
  # A failing setup must not fail the install, only print guidance.
  : > "$SETUP_LOG"; pty "$T/p5" "" FAKE_SETUP_EXIT=3
  [ "$PTY_RC" = 0 ] || fail "failing setup masked a good install (rc=$PTY_RC)"
  [ -x "$T/p5/oci-assume" ] || fail "binary missing after failing setup"
  grep -q 'setup did not finish' "$T/pty.out" || fail "no setup-failed guidance: $(cat "$T/pty.out")"
else
  echo "SKIP pty cases: script(1) not found"
fi

echo "PASS install"
