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

You need the [OCI CLI](https://docs.oracle.com/en-us/iaas/Content/API/SDKDocs/cliinstall.htm) first (`brew install oci-cli` on macOS).

### macOS

```sh
brew install --cask 123yogin/tap/oci-assume
```

or, without Homebrew:

```sh
curl -fsSL https://raw.githubusercontent.com/123yogin/oci-assume/main/install.sh | sh
```

### Linux

```sh
curl -fsSL https://raw.githubusercontent.com/123yogin/oci-assume/main/install.sh | sh
```

Or grab the `.deb` / `.rpm` from the [latest release](https://github.com/123yogin/oci-assume/releases/latest), or `go install github.com/123yogin/oci-assume/cmd/oci-assume@latest`.

### Windows (PowerShell)

Downloads the latest release, verifies its SHA-256 checksum and adds it to your user `PATH` (no admin needed):

```powershell
$v = (Invoke-RestMethod https://api.github.com/repos/123yogin/oci-assume/releases/latest).tag_name.TrimStart("v")
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$name = "oci-assume_${v}_windows_${arch}.zip"; $base = "https://github.com/123yogin/oci-assume/releases/download/v$v"
$dir = "$env:LOCALAPPDATA\Programs\oci-assume"
Invoke-WebRequest "$base/$name" -OutFile "$env:TEMP\$name"; Invoke-WebRequest "$base/checksums.txt" -OutFile "$env:TEMP\oci-assume-sums.txt"
$want = ((Get-Content "$env:TEMP\oci-assume-sums.txt") | Where-Object { $_ -match ("  " + [regex]::Escape($name) + "$") }).Split(" ")[0]
if ($want -ne (Get-FileHash "$env:TEMP\$name" -Algorithm SHA256).Hash.ToLower()) { throw "Checksum mismatch - not installing" }
Expand-Archive "$env:TEMP\$name" -DestinationPath $dir -Force
$p = [Environment]::GetEnvironmentVariable("Path", "User"); if ($p -notlike "*$dir*") { [Environment]::SetEnvironmentVariable("Path", "$p;$dir", "User") }; $env:Path += ";$dir"
oci-assume version
```

A one-line `irm … | iex` installer and Scoop support are coming in the next release.

## Get started

**1. Add the shell hook** (one time). Pick your shell:

| Shell | Run this once |
|---|---|
| zsh | `echo 'eval "$(oci-assume init zsh)"' >> ~/.zshrc && source ~/.zshrc` |
| bash | `echo 'eval "$(oci-assume init bash)"' >> ~/.bashrc && source ~/.bashrc` |
| fish | `echo 'oci-assume init fish \| source' >> ~/.config/fish/config.fish` |
| PowerShell | `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser; Add-Content $PROFILE 'oci-assume init pwsh \| Out-String \| Invoke-Expression'; . $PROFILE` |

The hook is a small shell function: a program can't change your shell's environment on its own, so this is what lets `oci-assume` set `OCI_CLI_PROFILE` for you.

**2. Log in once** (skip if you already have a session profile):

```sh
oci session authenticate --profile-name dev --region us-ashburn-1
```

**3. Use it:**

```sh
oci-assume                    # pick a profile from the list (or: oci-assume dev)
oci-assume daemon install     # keep sessions refreshed in the background
```

That's it. Sessions stay fresh in the background; you only log in through the browser again after Oracle's 24-hour limit. Check everything with `oci-assume doctor`.

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
   - Windows (PowerShell install): delete `%LOCALAPPDATA%\Programs\oci-assume` and remove it from your user PATH
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
