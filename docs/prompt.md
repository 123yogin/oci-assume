# Prompt

`oci-assume prompt` prints the active profile and the time left on its session, for example `dev 42m` or `dev 1h05m`. It prints `dev expired` when the session is dead, just the profile name for API-key profiles, and nothing when no profile is active.

It reads `OCI_CLI_PROFILE`, your OCI config and the token file. It never calls `oci` and makes no network calls of its own, so it is cheap enough to run on every prompt. It never fails: on any problem it prints nothing.

Add `--json` for a machine-readable form:

```json
{"profile":"dev","text":"dev 42m","state":"ok","remaining_seconds":2520}
```

`state` is `ok`, `warn` (under the refresh threshold, 10 minutes by default) or `expired`.

The shell hook from `oci-assume init` defines a helper that wraps this as `[oci dev 42m]` (or prints nothing). The snippets below use it.

## zsh

```zsh
setopt PROMPT_SUBST
PROMPT='$(oci_assume_prompt) %~ %# '
```

## bash

```bash
PS1='$(oci_assume_prompt) \w \$ '
```

## fish

```fish
function fish_right_prompt
    oci_assume_prompt
end
```

## PowerShell

```powershell
function prompt { "$(OciAssumePrompt) PS $($PWD.Path)> " }
```

## Starship

Add to `~/.config/starship.toml`:

```toml
[custom.oci]
command = "oci-assume prompt"
when = 'test -n "$OCI_CLI_PROFILE"'
format = "[oci $output]($style) "
style = "yellow"
```

## oh-my-posh

Add a segment to your theme:

```json
{
  "type": "command",
  "style": "plain",
  "foreground": "yellow",
  "template": "oci {{ .Output }} ",
  "properties": {
    "command": "oci-assume prompt"
  }
}
```

The segment hides itself when the output is empty.
