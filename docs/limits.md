# Limits

## The 24-hour refresh window

A session token is valid for 60 minutes. `oci session refresh` can extend it, but only for 24 hours after the browser login. After that, Oracle requires a new browser login. `oci-assume` cannot change this.

In practice: log in once in the morning and sessions stay fresh all day. When a session expires, `oci-assume <profile>` opens the browser; the background job only sends a notification.

## Running processes do not reload tokens

The `oci` CLI and SDKs read the token file once at startup. A process that is already running keeps using the old token and fails when it expires, even if `oci-assume` has refreshed the file. See [oracle/oci-cli#998](https://github.com/oracle/oci-cli/issues/998).

Keep this in mind for scripts and tools that run longer than the time left on the session.

## Terraform

The OCI Terraform provider must be told to use the session token. Point it at the profile:

```hcl
provider "oci" {
  auth                = "SecurityToken"
  config_file_profile = "dev"
  region              = "us-ashburn-1"
}
```

Run `oci-assume dev` (or `oci-assume refresh dev`) before `terraform plan` or `apply`. The provider reads the token once, so a long apply can still outlive it.

## Long-running jobs

Do not rely on session tokens for jobs that run for hours, or unattended. Use:

- an API key profile,
- instance principal authentication (on OCI compute), or
- resource principal authentication (for OCI Functions and similar).

These do not expire after an hour.

## Shell support

- zsh, bash, fish (3.1 or newer) and PowerShell are supported.
- bash in POSIX mode and `sh` are not supported: the hook defines a function named `oci-assume`, and a hyphen is not a valid function name there.
- Scripts do not load the hook. Name the shell with `--shell` and evaluate the output:

  ```sh
  eval "$(oci-assume --shell bash prod)"
  ```

  `--shell` takes `zsh`, `bash`, `fish` or `pwsh`. Without it (or the hook) the binary prints nothing to stdout and exits 1. Alternatively, call the `oci` CLI with `--profile` and `--auth security_token`.

## What setup changes

`oci-assume setup` never needs admin or sudo, never changes machine-wide policy, and only writes:

- your shell startup files (or the fish drop-in) listed in [how-it-works.md](how-it-works.md#setup-and-the-receipt), plus a one-time `.oci-assume.bak` next to each;
- the state directory;
- the scheduler's own definition (launchd plist, systemd user units or crontab line, or the Windows task);
- on Windows, your user `PATH` (`HKCU` only, by `install.ps1`) and, if you agree, your `CurrentUser` execution policy.

Limits:

- **Dotfile managers.** A symlinked startup file is written through to its target, so the change shows up in your dotfiles repo. A read-only file is refused with a message; add the line by hand.
- **Group Policy.** If Group Policy enforces a `Restricted` or `AllSigned` execution policy, setup cannot change it and the PowerShell hook will not load. Ask your administrator, or call the binary with `--shell pwsh`.
- **No shell detected.** When neither `--shell`, the parent process nor `$SHELL` names zsh, bash, fish or PowerShell, setup prints the line for each shell and skips the hook.
- **Linux OCI CLI.** Without Homebrew, setup prints Oracle's install command but does not run a third-party installer.
- **Non-interactive runs.** Without a terminal, or with `CI` set, setup needs `--yes` and then applies only the shell hook and background refresh. The OCI CLI install and the browser login need a terminal.
- **Containers and WSL without systemd** fall back to a crontab line; with neither, background refresh fails and the prompt tick is the only refresher.

## Profile names that match a subcommand

A profile called `status`, `login`, `refresh` or any other subcommand name is read as the subcommand. Put `--` first:

```sh
oci-assume -- status
```

## API-key and region-less profiles

Assuming a profile without a `security_token_file` clears `OCI_CLI_AUTH`, because API-key profiles must not use token auth. Assuming a profile with no `region` (its own or inherited from `[DEFAULT]`) clears `OCI_CLI_REGION`, so a region from the previous profile does not leak across. This also clears values you set yourself in the shell. Pass `-r <region>` to set one explicitly.
