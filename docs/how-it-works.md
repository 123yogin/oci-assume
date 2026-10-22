# How it works

## A shell function plus eval

A child process cannot change its parent shell's environment. So `oci-assume init <shell>` prints a small function that you load from your startup file. When you run `oci-assume dev`, the function:

1. runs the real binary with `OCI_ASSUME_EVAL=1`,
2. captures its stdout,
3. evaluates that output in your shell, but only if the binary exited 0.

The binary prints plain `export` / `unset` lines (or the fish and PowerShell equivalents), quoted for the target shell. The `init` output itself is static and contains no user data.

Some subcommands are passed straight through to the binary without eval: `status`, `refresh`, `login`, `prompt`, `daemon`, `init`, `doctor`, `setup`, `uninstall`, `version`, `help`, `completion`, `--help`, `-h` and `--version`.

The hook also defines a prompt helper (`oci_assume_prompt` in zsh, bash and fish; `OciAssumePrompt` in PowerShell). See [prompt.md](prompt.md).

## Setup and the receipt

`oci-assume setup` walks five steps in order: `oci-cli`, `exec-policy` (Windows only), `shell-hook`, `background` and `first-login`. Each step prints exactly what it will change (the file and the lines, or the command) and asks `[Y/n]`; a finished step prints `✓ already set up` and does not ask. Ctrl-C at a prompt stops setup with nothing further applied (exit 1).

- **oci-cli:** `brew install oci-cli`, or on Windows `winget install --id Oracle.OCI-CLI -e --scope user`. On Linux without Homebrew it only prints Oracle's install command.
- **exec-policy:** when PowerShell's effective policy is `Restricted` or `AllSigned`, offers `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser`. If Group Policy enforces it, setup explains and skips.
- **shell-hook:** writes one guarded line between `# >>> oci-assume >>>` and `# <<< oci-assume <<<` in `${ZDOTDIR:-~}/.zshrc`, `~/.bashrc` (on macOS, `~/.bash_profile` is also made to source it), each PowerShell's `$PROFILE.CurrentUserAllHosts`, or the fish drop-in `~/.config/fish/conf.d/oci-assume.fish`. The shell comes from `--shell`, then the parent process, then `$SHELL`. A file that already has an unmarked `oci-assume init` line counts as done. If no shell is found, setup prints the line for every shell and changes nothing.
- **background:** installs the scheduler job below.
- **first-login:** when no profile has a `security_token_file`, asks for a profile name and region and runs `oci session authenticate`.

Writes are atomic and keep the file's mode; a symlinked rc file is written through to its target. The first change to a file saves `<file>.oci-assume.bak`.

`<state dir>/receipt.json` (mode 0600) records the files setup wrote, the background backend and whether you agreed to it, and how the OCI CLI was installed. `oci-assume uninstall` reads it to remove exactly the marked blocks and drop-ins, the background job and the receipt. Without a receipt (a v1.0 user, or a corrupt file) it scans the known startup files for markers and removes the job anyway. `--purge` also deletes the state directory and the macOS log.

## stdout and stderr

- **stdout** carries only shell-evaluable lines on the assume and `--unset` paths, or command data for `prompt`, `status --json` and `init`. `setup`, `uninstall` and `doctor` also print their report to stdout (stderr under `OCI_ASSUME_EVAL=1`).
- **stderr** carries everything meant for a human: progress, errors and the fix command.

`OCI_ASSUME_EVAL=1` is the guard. The hook sets it only on the eval path. When it is set, the binary routes all non-export stdout, such as `--help` text, to stderr, so nothing but export lines can reach `eval`.

The assume and `--unset` paths print exports only when `OCI_ASSUME_EVAL=1` is set (the hook's shell, from `OCI_ASSUME_SHELL`, picks the dialect) or when `--shell zsh|bash|fish|pwsh` names one explicitly. `OCI_ASSUME_SHELL` alone is not enough, because it is inherited by child shells that never loaded the hook. Otherwise the binary prints the hook line to stderr and exits 1 with empty stdout.

## Ensuring a session

For a profile with a `security_token_file`, `oci-assume` runs this sequence (`--threshold` and `OCI_ASSUME_THRESHOLD` change the 10-minute default):

```
no security_token_file          -> OK (API-key profile; never refreshed)
acquire the profile's lock (10 s timeout)
  on timeout: re-read the token; more than the threshold left -> OK,
              otherwise exit 5
remaining > threshold           -> OK, nothing to do
token still valid               -> oci session refresh
                                     success -> OK
                                     "expired"/"not valid" -> LOGIN
                                     other failure -> LOGIN if interactive,
                                                      else exit 1 (no notification)
expired or missing              -> LOGIN
LOGIN, interactive              -> oci session authenticate --profile-name P [--region R]
LOGIN, background               -> notify once per profile per 24 h; exit 2
```

`refresh --background` uses a threshold of at least 45 minutes, so a job that runs every 30 minutes always refreshes a 60-minute token before it lapses.

The `oci` child runs without `OCI_CLI_PROFILE`, `OCI_CLI_AUTH` and `OCI_CLI_REGION`, so the profile you have assumed never overrides the one being refreshed.

All credential changes go through the official `oci` CLI. `oci-assume` reads the config file and token directly only for read-only work: listing, expiry and the prompt. It reads a token's `exp` claim and nothing else, and does not verify signatures.

The `oci` binary is found through `$OCI_ASSUME_OCI_BIN`, then `PATH`. The config file is `$OCI_CLI_CONFIG_FILE`, else `~/.oci/config`.

## Locks and the state directory

- Each profile has an advisory file lock under `<state dir>/locks/`, so two terminals (or the background job and a terminal) never refresh the same profile at once.
- `<state dir>/state.json` records when each profile was last assumed and the result of the last refresh. `refresh --all` uses it to pick profiles; `doctor` and `daemon status` print it. Writes are atomic.

The state directory is `$OCI_ASSUME_STATE_DIR` if set, otherwise `oci-assume` inside your OS user config directory (`~/Library/Application Support` on macOS, `~/.config` on Linux, `%AppData%` on Windows).

There is no fallback to a shared temp directory. If neither is available (for example `HOME` is unset), commands that need state fail with `cannot determine state directory; set OCI_ASSUME_STATE_DIR` (`ErrNoStateDir` in the code).

## Background scheduler

`setup` (or `oci-assume daemon install`) registers a job that runs `oci-assume refresh --all --background` every 30 minutes and once right away. It never opens a browser. If a session has expired it sends a desktop notification, at most once per profile per 24 hours.

| OS | Mechanism |
|---|---|
| macOS | launchd agent `io.github.123yogin.oci-assume` in `~/Library/LaunchAgents`; log at `~/Library/Logs/oci-assume.log` |
| Linux | systemd user timer `oci-assume.timer` (and `oci-assume.service`); falls back to a crontab line marked `# oci-assume` when `systemctl --user` is unavailable |
| Windows | Task Scheduler task `oci-assume-refresh`, every 30 minutes and at logon; it runs the windowless `oci-assume-bg.exe` next to `oci-assume.exe`, so no console window flashes. `go install` builds only `oci-assume.exe`, so the task runs it directly and a console window briefly flashes each run |

The entry records `PATH`, plus `OCI_CLI_CONFIG_FILE`, `OCI_ASSUME_STATE_DIR` and `OCI_ASSUME_OCI_BIN` when they are set at install time, so the job sees the same config as your shell. Re-run `daemon install` after changing them. The cron line discards output; results go to the state file.

`oci-assume daemon status` shows whether the job is installed and the last background result per profile (manual `refresh` runs are not listed). `oci-assume daemon uninstall` removes it and records that you turned it off.

### Self-heal

If the receipt says you agreed to background refresh, every `oci-assume` command (except `prompt`, `init`, `setup`, `uninstall`, `daemon`, `doctor`, `version`, `help` and `completion`) checks the job. When it is missing, cannot be read, or points at a binary that no longer exists (for example after a moved or deleted install), the command reinstalls it silently. The check runs at most once per 10 minutes, and failures go to the log, never to your terminal. A job you declined or removed with `daemon uninstall` is never reinstalled.

### Prompt tick

The hook's prompt helper runs `oci-assume prompt --tick`. Its output is the same as `prompt`, but when the active profile is a session profile with less than 45 minutes left, it also starts a detached `oci-assume refresh <profile> --background` and returns at once. It starts at most one per profile every 5 minutes (a timestamp file in the state directory), skips when another refresh holds the profile's lock, and never runs `oci` in the prompt process. This keeps sessions alive while you work even where no scheduler is available. Only the hook's prompt helper (`oci_assume_prompt`, `OciAssumePrompt`) and prompts that call `oci-assume prompt --tick` themselves get the tick; plain `oci-assume prompt` never starts a refresh.

Notifications use `osascript` (macOS), `notify-send` (Linux) or a PowerShell toast (Windows), and are skipped silently when none is available.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | OK |
| 1 | Generic error (also Ctrl-C) |
| 2 | Needs a browser login |
| 3 | `oci` CLI not found |
| 4 | Config error (unknown profile, bad flag value, unreadable config, insecure file permissions, no state directory) |
| 5 | Lock timeout (another `oci-assume` is refreshing or logging in to the profile) |
