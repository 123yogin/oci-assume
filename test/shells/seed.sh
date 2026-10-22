#!/bin/sh
# Usage: seed.sh dev|norgn|hostile. Writes a fresh 0600 token via the fake oci.
FAKE_TOKEN_PATH="$TESTDIR/$1.token" FAKE_OCI_MODE=auth-ok "$OCI_ASSUME_OCI_BIN" session authenticate >/dev/null
