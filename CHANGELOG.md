# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/123yogin/oci-assume/commits/main
