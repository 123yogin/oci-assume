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
printf '#!/bin/sh\necho fake-oci-assume\n' > "$T/pkg/oci-assume"
chmod 755 "$T/pkg/oci-assume"
tar -czf "$T/rel/$archive" -C "$T/pkg" oci-assume
(cd "$T/rel" && sha "$archive" | awk '{ print $1 "  " $2 }' > checksums.txt)

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

echo "PASS install"
