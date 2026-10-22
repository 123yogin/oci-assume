# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through [GitHub Security Advisories](https://github.com/123yogin/oci-assume/security/advisories/new). Do not open a public issue.

Include the version (`oci-assume version`), your OS and shell, and steps to reproduce. You will get a reply as soon as the maintainer can, and a fix will be released before the details are made public.

## Supported versions

Only the latest minor release receives security fixes.

## Security properties

- oci-assume never reads or writes private keys, and never writes tokens. It only reads a token's `exp` claim.
- It makes no network calls of its own and has no telemetry. All credential changes go through the official `oci` CLI.
- It refuses to use a token or key file that other users can read or write (Unix: mode `& 0o077 != 0`). `oci-assume doctor` explains the fix.
- `oci-assume init` output is static and contains no user data, so evaluating it is safe.
- Supply chain: GitHub Actions are pinned by SHA, CI runs `govulncheck` and, on pull requests, dependency review (fails on moderate or higher severity), and releases are built with GoReleaser, signed with cosign (keyless) and published with an SBOM and SHA-256 checksums.

## Verify a release

Download `checksums.txt` and `checksums.txt.sigstore.json` from the release, then:

```sh
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/123yogin/oci-assume/\.github/workflows/release\.yml@refs/tags/v.*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

`install.sh` verifies the SHA-256 checksum of the archive it downloads, but not the signature; use the `cosign verify-blob` step above to verify the signature of `checksums.txt`. Then check your archive against it, for example `sha256sum --ignore-missing -c checksums.txt`.
