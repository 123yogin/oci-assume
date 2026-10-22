# oci-assume

Switch OCI CLI profiles and keep browser (SSO) session tokens fresh, the way Granted's `assume` does for AWS.

## Why

If you sign in with `oci session authenticate`, you know the routine:

- The session token lasts 60 minutes, and the `oci` CLI never refreshes it for you.
- You have to remember `oci session refresh` before it runs out.
- Every command needs `--profile` and `--auth security_token`.

`oci-assume <profile>` checks the session, refreshes it (or opens the browser login if it has expired), and sets `OCI_CLI_PROFILE`, `OCI_CLI_AUTH` and `OCI_CLI_REGION` in your current shell. A background job keeps recently used sessions alive. After that, plain `oci` commands just work.

It wraps the official `oci` CLI for everything that writes credentials. It never touches your private keys and makes no network calls of its own.

## Install

Homebrew (macOS only; on Linux use curl, deb/rpm or `go install` below):

```sh
brew install --cask 123yogin/tap/oci-assume
```

Scoop (Windows):

```powershell
scoop bucket add 123yogin https://github.com/123yogin/scoop-bucket
scoop install oci-assume
```

curl (macOS and Linux; verifies the SHA-256 checksum, installs to `~/.local/bin`):

```sh
curl -fsSL https://raw.githubusercontent.com/123yogin/oci-assume/main/install.sh | sh
```

Set `OCI_ASSUME_INSTALL_DIR` to install somewhere else, or pass `--version vX.Y.Z` to pin a release (`sh -s -- --version vX.Y.Z`).

Debian, Ubuntu, Fedora and friends: download the `.deb` or `.rpm` from the [releases page](https://github.com/123yogin/oci-assume/releases) and install it with `dpkg -i` or `rpm -i`.

With Go:

```sh
go install github.com/123yogin/oci-assume/cmd/oci-assume@latest
```

You also need the [OCI CLI](https://docs.oracle.com/en-us/iaas/Content/API/SDKDocs/cliinstall.htm) on your `PATH` (or set `OCI_ASSUME_OCI_BIN`).

## Quickstart

1. Add the hook to your shell startup file. The hook is a small function; a child process cannot change your shell's environment, so it has to live in the shell.

   | Shell | Line to add |
   |---|---|
   | zsh (`~/.zshrc`) | `eval "$(oci-assume init zsh)"` |
   | bash (`~/.bashrc`) | `eval "$(oci-assume init bash)"` |
   | fish (`~/.config/fish/config.fish`) | `oci-assume init fish \| source` |
   | PowerShell (`$PROFILE`) | `oci-assume init pwsh \| Out-String \| Invoke-Expression` |

2. Open a new shell and run `oci-assume`. Pick a profile from the list, or name one: `oci-assume dev`. If the session needs a login, your browser opens.

3. Keep sessions fresh in the background:

   ```sh
   oci-assume daemon install
   ```

Check your setup any time with `oci-assume doctor`.

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
| `oci-assume daemon install\|uninstall\|status` | Manage the background refresh job (every 30 minutes). |
| `oci-assume init zsh\|bash\|fish\|pwsh` | Print the shell hook. |
| `oci-assume doctor` | Check the `oci` CLI, config, file permissions, shell hook and daemon. |
| `oci-assume version` | Print the version. |
| `oci-assume completion <shell>` | Generate shell completions. |

Flags available on every command: `--threshold <duration>` (refresh when less than this remains; default `10m`, or set `OCI_ASSUME_THRESHOLD`) and `--login-timeout <duration>` (how long to wait for the browser login; default `5m`).

## Uninstall

1. Remove the background job: `oci-assume daemon uninstall`.
2. Delete the `oci-assume init ...` line from your shell startup file.
3. Delete the state directory (or `$OCI_ASSUME_STATE_DIR` if you set it):
   - macOS: `~/Library/Application Support/oci-assume`
   - Linux: `~/.config/oci-assume` (or `$XDG_CONFIG_HOME/oci-assume`)
   - Windows: `%AppData%\oci-assume`
4. macOS only: delete the log file `~/Library/Logs/oci-assume.log`.
5. Remove the binary with the tool you installed it with:
   - Homebrew: `brew uninstall --cask oci-assume`
   - Scoop: `scoop uninstall oci-assume`
   - deb/rpm: `sudo apt remove oci-assume` or `sudo dnf remove oci-assume`
   - curl: `rm ~/.local/bin/oci-assume` (or the file in `$OCI_ASSUME_INSTALL_DIR`)
   - Go: `rm "$(go env GOPATH)/bin/oci-assume"`

Your `~/.oci` config, keys and tokens are left untouched.

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
