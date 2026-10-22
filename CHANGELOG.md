# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.1.0] - 2026-10-23

One-command onboarding: install, then everything else is `oci-assume setup`.

### Added

- `oci-assume setup`: offers, in order, the OCI CLI install (Homebrew or winget), the Windows execution policy, the shell hook, background refresh and a first browser login. Each step shows the exact file change or command and asks first; finished steps are skipped. `--yes`, `--dry-run`, `--no-shell`, `--no-background`, `--no-oci-cli`, `--no-login` and `--shell`.
- `oci-assume uninstall [--purge]`: removes exactly what setup added, using `receipt.json` in the state directory, or by scanning the known startup files when there is no receipt. Safe to run twice.
- When `oci-assume` on `PATH` is not the binary running setup, the hook block also puts its directory on `PATH`, and setup warns if new shells still will not find it.
- Marked, reversible shell hook blocks (`# >>> oci-assume >>>`) for zsh, bash (with macOS `.bash_profile` sourcing), fish (`conf.d` drop-in) and every installed PowerShell's `$PROFILE.CurrentUserAllHosts`. Existing unmarked hook lines are respected; symlinked rc files are written through.
- `install.ps1`: `irm https://raw.githubusercontent.com/123yogin/oci-assume/main/install.ps1 | iex` downloads and checksum-verifies the release, adds it to the user `PATH` and runs setup.
- `install.sh` runs setup when a terminal is available; new `--no-setup` and `--yes` flags.
- Self-healing background refresh: when you agreed to it, any command reinstalls a missing or broken job (at most once per 10 minutes).
- Prompt tick: the hook's prompt helper starts a detached refresh when the active session has under 45 minutes left (at most once per 5 minutes per profile).
- Windows: the scheduled task also runs at logon and uses the windowless `oci-assume-bg.exe`, so no console window flashes.
- The picker offers `+ New profile…`, and bare `oci-assume` with no session profiles offers to create one.
- `doctor` also checks the hook block in your shell's startup file, the setup receipt, the background job's binary path and (Windows) the execution policy.
- Homebrew caveats, Scoop `post_install`, winget release notes and the deb/rpm post-install script point to `oci-assume setup`.

### Changed

- `daemon install` is no longer part of the documented getting-started path; `setup` turns background refresh on by default. `daemon uninstall` now records that you turned it off, so self-heal leaves it alone.
- `doctor` suggests `oci-assume setup` for a missing hook or background job, and no longer self-heals before checking.

### Fixed

- `go install` builds report their module version in `oci-assume version`.

## [1.0.0] - 2026-10-22

### Added

- `oci-assume [profile]`: pick a profile (fuzzy picker on a TTY; without one, it lists the profile names and exits 4), ensure its session is valid, and set `OCI_CLI_PROFILE`, `OCI_CLI_AUTH` and `OCI_CLI_REGION` in the current shell.
- `--shell zsh|bash|fish|pwsh` to print exports without the hook, for scripts: `eval "$(oci-assume --shell bash prod)"`.
- Automatic refresh before expiry, with browser login (`oci session authenticate`) when the session has expired. Configurable with `--threshold` and `OCI_ASSUME_THRESHOLD`.
- `-r/--region` override and `--unset`.
- `status [--json]`, `refresh [profile|--all] [--background]`, `login <profile>`, `prompt [--json]`, `doctor`, `version` and `completion`.
- `init` shell hooks for zsh, bash, fish and PowerShell.
- `daemon install|uninstall|status`: background refresh every 30 minutes with launchd (macOS), a systemd user timer with a cron fallback (Linux) or Task Scheduler (Windows), plus desktop notifications when a login is needed.
- Prompt segment with no network calls; snippets for zsh, bash, fish, PowerShell, Starship and oh-my-posh.
- Per-profile file locks and atomic state file.
- Refusal to use token or key files readable by other users.
- Releases for macOS, Linux and Windows (amd64 and arm64) through GitHub Releases, Homebrew, Scoop, winget manifests, `.deb`/`.rpm` and `install.sh`, with cosign-signed checksums and SBOMs.

[Unreleased]: https://github.com/123yogin/oci-assume/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/123yogin/oci-assume/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/123yogin/oci-assume/releases/tag/v1.0.0
