#!/bin/sh
# Builds a linux binary, then runs each shell's case script in a container.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
cd "$root"
mkdir -p "$here/.bin"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$here/.bin/oci-assume" ./cmd/oci-assume
docker build --platform linux/amd64 -q -t oci-assume-shells "$here" >/dev/null

fail=0
for sh in zsh bash fish pwsh; do
  if docker run --rm --platform linux/amd64 -v "$root":/src:ro \
      -e SHELLNAME="$sh" oci-assume-shells sh /src/test/shells/inside.sh; then
    echo "PASS $sh"
  else
    echo "FAIL $sh"; fail=1
  fi
done
exit $fail
