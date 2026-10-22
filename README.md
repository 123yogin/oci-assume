# oci-assume

[![CI](https://github.com/123yogin/oci-assume/actions/workflows/ci.yml/badge.svg)](https://github.com/123yogin/oci-assume/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/123yogin/oci-assume)](https://github.com/123yogin/oci-assume/releases/latest)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

**Switch OCI CLI profiles and never refresh a session token by hand again.** Like Granted's `assume` for AWS, but for Oracle Cloud.

```console
$ oci-assume                      # pick a profile
$ oci iam region list             # no --profile, no --auth, it just works
$ oci-assume status
PROFILE       REGION        EXPIRES  STATE
dev           us-ashburn-1  52m      ● active
```

## Install

One command per OS. It installs `oci-assume`, then runs `oci-assume setup`, which offers each step and asks before changing anything:

- installs the OCI CLI if it is missing;
- adds the shell hook to your shell's startup file;
- turns on background refresh;
- creates your first session profile through the browser.

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/123yogin/oci-assume/main/install.sh | sh
```

**Windows (PowerShell, no admin needed)**

```powershell
irm https://raw.githubusercontent.com/123yogin/oci-assume/main/install.ps1 | iex
```

**Package managers**: install, then run `oci-assume setup`.

| Tool | Install |
|---|---|
| Homebrew (macOS) | `brew install --cask 123yogin/tap/oci-assume` |
| Scoop (Windows) | `scoop bucket add 123yogin https://github.com/123yogin/scoop-bucket; scoop install oci-assume` |
| deb / rpm | download from the [latest release](https://github.com/123yogin/oci-assume/releases/latest), then `sudo apt install ./oci-assume_*.deb` or `sudo dnf install ./oci-assume-*.rpm` |
| Go | `go install github.com/123yogin/oci-assume/cmd/oci-assume@latest` |

Then open a new terminal and run `oci-assume`. Re-running `oci-assume setup` is safe: finished steps show `✓ already set up`. Check everything with `oci-assume doctor`.

`setup --dry-run` prints the plan without changing anything. Without a terminal (or with `CI` set) it needs `--yes`, and then applies only the shell hook and background refresh. `--no-shell`, `--no-background`, `--no-oci-cli`, `--no-login` and `--shell zsh|bash|fish|pwsh` narrow it.

## Use it

```sh
oci-assume                    # pick a profile from the list (or: oci-assume dev)
oci iam region list           # no --profile, no --auth
```

Sessions stay fresh in the background; you only log in through the browser again after Oracle's 24-hour limit.

## Advanced / manual setup

`setup` only automates these steps. To do them by hand instead:

1. **Shell hook.** Add one line to your startup file. A program can't change your shell's environment on its own, so this small function is what lets `oci-assume` set `OCI_CLI_PROFILE` for you.

   | Shell | Line | File |
   |---|---|---|
   | zsh | `eval "$(oci-assume init zsh)"` | `~/.zshrc` |
   | bash | `eval "$(oci-assume init bash)"` | `~/.bashrc` |
   | fish | `oci-assume init fish \| source` | `~/.config/fish/config.fish` |
   | PowerShell | `oci-assume init pwsh \| Out-String \| Invoke-Expression` | `$PROFILE.CurrentUserAllHosts` (run `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser` first if scripts are blocked) |

2. **Log in once:** `oci session authenticate --profile-name dev --region us-ashburn-1`.
3. **Background refresh:** `oci-assume daemon install` (and `daemon uninstall` / `daemon status`).

## Commands

| Command | What it does |
|---|---|
| `oci-assume [profile]` | Pick a profile (or name one), make sure its session is valid, and set the OCI variables in this shell. |
| `oci-assume -r <region> [profile]` | Same, overriding the profile's region. |
| `oci-assume --unset` | Clear `OCI_CLI_PROFILE`, `OCI_CLI_AUTH` and `OCI_CLI_REGION`. |
| `eval "$(oci-assume --shell bash <profile>)"` | Use from a script, without the hook. `--shell` takes `zsh`, `bash`, `fish` or `pwsh`. |
| `oci-assume status [--json]` | Show every profile's region and session expiry. |
| `oci-assume refresh [profile]` | Refresh a session. Defaults to `$OCI_CLI_PROFILE`. |
| `oci-assume refresh --all [--background]` | Refresh every profile assumed in the last 24 hours. `--background` never opens a browser; it sends a notification instead. |
| `oci-assume login <profile>` | Force a browser login (`oci session authenticate`). |
| `oci-assume prompt [--json]` | Print the active profile and time left, for shell prompts. |
| `oci-assume setup [--yes] [--dry-run]` | Install the OCI CLI, shell hook and background refresh, then log in. Asks before each step. |
| `oci-assume uninstall [--purge]` | Undo everything `setup` changed. `--purge` also deletes the state directory and log. |
| `oci-assume daemon install\|uninstall\|status` | Manage the background refresh job by hand (every 30 minutes). |
| `oci-assume init zsh\|bash\|fish\|pwsh` | Print the shell hook. |
| `oci-assume doctor` | Check the `oci` CLI, config, file permissions, shell hook, setup receipt, background job and (Windows) execution policy. |
| `oci-assume version` | Print the version. |
| `oci-assume completion <shell>` | Generate shell completions. |

Flags available on every command: `--threshold <duration>` (refresh when less than this remains; default `10m`, or set `OCI_ASSUME_THRESHOLD`) and `--login-timeout <duration>` (how long to wait for the browser login; default `5m`).

## Uninstall

```sh
oci-assume uninstall           # add --purge to also delete the state directory and log
```

It removes the shell hook blocks, the background job and the setup receipt, and is safe to run twice. Then remove the binary with the tool you installed it with (`uninstall` prints the commands):

- Homebrew: `brew uninstall --cask oci-assume`
- Scoop: `scoop uninstall oci-assume`
- Windows (`install.ps1`): delete `%LOCALAPPDATA%\Programs\oci-assume` and remove it from your user PATH
- deb/rpm: `sudo apt remove oci-assume` or `sudo dnf remove oci-assume`
- curl: `rm ~/.local/bin/oci-assume` (or the file in `$OCI_ASSUME_INSTALL_DIR`)
- Go: `rm "$(go env GOPATH)/bin/oci-assume"`

If `setup` installed the OCI CLI, `uninstall` prints its removal command too, but never runs it. Your `~/.oci` config, keys and tokens are left untouched.

## Prompt

`oci-assume prompt` reads your config and token file only. It never runs `oci`, so it is safe to call on every prompt. Ready-made snippets for zsh, bash, fish, PowerShell, Starship and oh-my-posh are in [docs/prompt.md](docs/prompt.md).

## Comparison with Granted

| | Granted (`assume`) | oci-assume |
|---|---|---|
| Cloud | AWS | Oracle Cloud Infrastructure |
| Sets credentials in the current shell | Yes, through a shell function | Yes, through a shell function |
| Interactive profile picker | Yes | Yes |
| Handles browser SSO login | Yes | Yes, through `oci session authenticate` |
| Refreshes before expiry | When you assume | When you assume, and in the background |
| Background refresh job | No daemon; re-assumed on demand (shell recipe available) | Yes: launchd, systemd user timer (cron fallback) or Task Scheduler |
| Prompt segment | No built-in prompt segment (as far as its docs show) | `oci-assume prompt`, no network calls |
| Opens the web console | Yes | Not yet |
| How credentials are produced | Its own credential process | The official `oci` CLI; oci-assume never writes tokens or keys |
| Session ceiling | Set by AWS | Oracle's 24-hour refresh window; one browser login per day |

## Limits

- A session can be refreshed for 24 hours after the browser login. After that you log in again.
- Programs that are already running do not pick up a refreshed token.
- Terraform needs `auth = "SecurityToken"`; long jobs should use API keys or instance or resource principals.
- bash in POSIX mode and `sh` are not supported.

Details and workarounds: [docs/limits.md](docs/limits.md). How the pieces fit together: [docs/how-it-works.md](docs/how-it-works.md).

## Security

oci-assume never reads or writes private keys, never writes tokens (it only reads a token's expiry), makes no network calls of its own and has no telemetry. See [SECURITY.md](SECURITY.md) to report a vulnerability or verify a release.

## Contributing

Issues and pull requests are welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md). Everyone taking part is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
