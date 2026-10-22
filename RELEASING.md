# Releasing oci-assume

## One-time setup

1. Create the GitHub repos `123yogin/oci-assume`, `123yogin/homebrew-tap` and `123yogin/scoop-bucket` (the tap and bucket can start empty).
2. Create two fine-grained PATs, each scoped to one repo with `Contents: Read and write`:
   - tap token on `123yogin/homebrew-tap`
   - bucket token on `123yogin/scoop-bucket`
3. In `123yogin/oci-assume` add Actions secrets `HOMEBREW_TAP_TOKEN` and `SCOOP_BUCKET_TOKEN` with those values.
4. Optionally fork `microsoft/winget-pkgs` to `123yogin/winget-pkgs` for the winget PR.

## Release steps

1. Make sure `main` is green in CI.
2. Tag and push: `git tag v1.2.3 && git push origin v1.2.3`.
3. Watch the `release` workflow. It builds 6 archives, `checksums.txt`, its cosign Sigstore bundle, SBOMs, deb/rpm packages, and updates the tap and bucket.
4. Verify the GitHub release lists the archives, `checksums.txt` and `checksums.txt.sigstore.json` (the cosign bundle).
5. Verify the signature (keyless, identity is the release workflow at the tag):

   ```sh
   cosign verify-blob \
     --bundle checksums.txt.sigstore.json \
     --certificate-identity-regexp '^https://github\.com/123yogin/oci-assume/\.github/workflows/release\.yml@refs/tags/v.*$' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     checksums.txt
   ```

6. Smoke test: `brew install --cask 123yogin/tap/oci-assume` and `oci-assume version`; `scoop bucket add 123yogin https://github.com/123yogin/scoop-bucket && scoop install oci-assume`.

## winget

The release only generates the manifest (`skip_upload: true`) and attaches it to the workflow run as the `winget-manifests` artifact.

1. Download `winget-manifests` from the release workflow run (Actions tab, or `gh run download <run-id> -n winget-manifests`).
2. Copy the files to `manifests/y/123yogin/oci-assume/<version>/` in your `winget-pkgs` fork.
3. Open a PR to `microsoft/winget-pkgs` and fix any validation feedback.

## Release checklist

CI covers the fake `oci` and the scheduler commands. Check these by hand against a real tenancy before the first tag, and again after changes to the area they cover.

- [ ] **Assume:** `oci-assume <session-profile>`, then `oci iam region list` with no flags succeeds.
- [ ] **Status:** `oci-assume status` agrees with `oci session validate --profile <p>` for a fresh and an expired session.
- [ ] **Picker:** `oci-assume` with no profile on a real terminal shows the fuzzy picker; arrows, filter, Enter and Esc work.
- [ ] **Prompt timing:** `hyperfine -N --warmup 20 'oci-assume prompt'` stays in the low milliseconds (no `oci` call).
- [ ] **Daemon:** `oci-assume daemon install`, wait at least 35 minutes, and check the profile's expiry advanced (`oci-assume status`, `oci-assume daemon status`). Then `oci-assume daemon uninstall` and confirm no scheduler entry is left (`launchctl print gui/$(id -u)/io.github.123yogin.oci-assume` fails; no `oci-assume.timer` unit and no `# oci-assume` line in `crontab -l`; `schtasks /Query /TN oci-assume-refresh` fails).
- [ ] **Homebrew upgrade:** `brew install --cask 123yogin/tap/oci-assume`, `oci-assume daemon install`, upgrade to the next release, and confirm the job still runs (it points at `$(brew --prefix)/bin/oci-assume`, not the versioned Caskroom path).
- [ ] **Signature:** run the cosign `verify-blob` command above against the first tag's `checksums.txt.sigstore.json`.
- [ ] **Expired-session wording:** let a session expire and confirm the real `oci session refresh` / `validate` stderr contains "expired" or "not valid" (the patterns `internal/ocicli` maps to `ErrNeedsLogin`). Update the patterns if Oracle's wording differs.
- [ ] **Demo:** record `docs/demo.gif` with `vhs docs/demo.tape`, then add it back to the top of the README.
