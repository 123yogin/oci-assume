#!/usr/bin/env bash
# Fresh-machine end-to-end checks for a published oci-assume release (macOS/Linux).
# Usage: e2e.sh <step>... | all   (steps: prepare install setup assert-setup shell commands background uninstall zap)
# Outside CI it uses a temp HOME and stubs the scheduler unless E2E_ALLOW_LAUNCHD=1 / E2E_ALLOW_SCHEDULER=1.
# `a && ok || fail` is intended (ok never fails); single-quoted $ is literal shell text.
# shellcheck disable=SC2015,SC2016
set -uo pipefail

LABEL=io.github.123yogin.oci-assume
MARK_BEGIN='# >>> oci-assume >>>'
RC_FILES=".zshrc .bashrc .bash_profile .bash_login .profile .config/fish/conf.d/oci-assume.fish"
here=$(cd "$(dirname "$0")" && pwd)
FAILS=0

ok() { printf 'ok   %s\n' "$*"; }
fail() { printf 'FAIL %s\n' "$*"; FAILS=$((FAILS + 1)); }
skip() { printf 'skip %s\n' "$*"; }
die() { printf 'FAIL %s\n' "$*"; exit 1; }
# check <what> <cmd...>: ok when the command succeeds.
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then ok "$what"; else fail "$what"; fi; }

: "${E2E_WORK:?set E2E_WORK to a scratch directory}"
ENVF="$E2E_WORK/env"
OS=$(uname -s)

sched_allowed() {
  case "$OS" in
    Darwin) [ "${E2E_ALLOW_LAUNCHD:-}" = 1 ] ;;
    *) [ "${E2E_ALLOW_SCHEDULER:-}" = 1 ] ;;
  esac
}

state_dir() {
  case "$OS" in
    Darwin) printf '%s\n' "$HOME/Library/Application Support/oci-assume" ;;
    *) printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/oci-assume" ;;
  esac
}

OA() { "$E2E_BIN_DIR/oci-assume" "$@"; }
# v1.0 has no setup subcommand (its root would treat "setup" as a profile).
has_setup() { OA --help 2>/dev/null | grep -qE '^ +setup '; }
need_setup() { has_setup || { skip "$1 (needs v1.1 setup; $E2E_VERSION has none)"; return 1; }; }

# Writes a stub so a local run can never load, boot out or kickstart a real scheduler entry.
stub() {
  printf '#!/bin/sh\n# e2e stub: the real %s is off limits outside CI\n%s\n' "$1" "$2" >"$E2E_WORK/bin/$1"
  chmod 755 "$E2E_WORK/bin/$1"
}

step_prepare() {
  : "${E2E_VERSION:?set E2E_VERSION to the tag under test, e.g. v1.1.0}"
  local real_home=$HOME src=${E2E_FAKE_SRC:-$here/../..}
  mkdir -p "$E2E_WORK/bin" "$E2E_WORK/snap"
  # Build before HOME moves so the Go cache stays warm.
  (cd "$src" && go build -o "$E2E_WORK/bin/oci" ./internal/testutil/fakeoci/cmd/oci) || die "build fake oci from $src"
  ok "fake oci built from $src"

  if [ "${CI:-}" != true ]; then
    HOME="$E2E_WORK/home"
    mkdir -p "$HOME"
  fi
  case "$HOME" in "$E2E_WORK"/*) ;; *) [ "${CI:-}" = true ] || die "refusing to use real HOME $real_home" ;; esac

  local path=""
  if [ "${CI:-}" = true ]; then
    # Drop ~/.local/bin so the hook block must add it, as on a new machine.
    path=$(printf '%s' "$PATH" | tr ':' '\n' | grep -vxF "$HOME/.local/bin" | paste -sd: -)
  else
    path=/usr/bin:/bin:/usr/sbin:/sbin
  fi
  PATH="$E2E_WORK/bin:$path"
  if ! sched_allowed; then
    case "$OS" in
      Darwin) stub launchctl 'case "$1" in print) echo "Could not find service" >&2; exit 113;; esac; echo "e2e stub: launchctl $* blocked" >&2; exit 1' ;;
      *) stub systemctl 'exit 1'; stub crontab 'echo "no crontab for e2e" >&2; exit 1' ;;
    esac
    ok "scheduler stubbed (set E2E_ALLOW_LAUNCHD=1 / E2E_ALLOW_SCHEDULER=1 to use the real one)"
  fi

  case "${E2E_CHANNEL:-script}" in
    brew) E2E_BIN_DIR="$(brew --prefix)/bin" ;;
    *) E2E_BIN_DIR="$HOME/.local/bin" ;;
  esac
  local shells=${E2E_SHELLS:-}
  if [ -z "$shells" ]; then
    shells=bash
    command -v zsh >/dev/null 2>&1 && shells="zsh bash"
  fi

  # A realistic config: one session profile the fake mints tokens for, one API-key profile.
  local d="$HOME/.oci" s="$HOME/.oci/sessions/e2e"
  mkdir -p "$s" && chmod 700 "$d" "$d/sessions" "$s"
  (umask 077 && printf -- '-----BEGIN PRIVATE KEY-----\nZTJlLWZha2U=\n-----END PRIVATE KEY-----\n' | tee "$s/oci_api_key.pem" >"$d/e2e-key.pem")
  cat >"$d/config" <<EOF
[e2e]
fingerprint=12:34:56:78:9a:bc:de:f0:12:34:56:78:9a:bc:de:f0
key_file=~/.oci/sessions/e2e/oci_api_key.pem
tenancy=ocid1.tenancy.oc1..aaaaaaaae2etenancy
region=us-ashburn-1
security_token_file=~/.oci/sessions/e2e/token

[e2e-key]
user=ocid1.user.oc1..aaaaaaaae2euser
fingerprint=fe:dc:ba:98:76:54:32:10:fe:dc:ba:98:76:54:32:10
key_file=~/.oci/e2e-key.pem
tenancy=ocid1.tenancy.oc1..aaaaaaaae2etenancy
region=us-phoenix-1
EOF
  chmod 600 "$d/config"
  FAKE_TOKEN_PATH="$s/token"
  export HOME PATH FAKE_TOKEN_PATH
  oci session authenticate --profile-name e2e --region us-ashburn-1 >/dev/null || die "fake oci session authenticate"
  check "session token seeded at $FAKE_TOKEN_PATH" test -s "$FAKE_TOKEN_PATH"

  local f
  for f in $RC_FILES; do
    if [ -e "$HOME/$f" ]; then cp -p "$HOME/$f" "$E2E_WORK/snap/${f//\//_}"; else : >"$E2E_WORK/snap/${f//\//_}.absent"; fi
  done
  ok "snapshot of rc files taken"

  {
    printf 'export %s=%q\n' HOME "$HOME" PATH "$PATH" FAKE_TOKEN_PATH "$FAKE_TOKEN_PATH" \
      E2E_VERSION "$E2E_VERSION" E2E_BIN_DIR "$E2E_BIN_DIR" E2E_SHELLS "$shells" \
      E2E_CHANNEL "${E2E_CHANNEL:-script}"
    printf 'unset OCI_CLI_PROFILE OCI_CLI_AUTH OCI_CLI_REGION OCI_CLI_CONFIG_FILE OCI_ASSUME_STATE_DIR OCI_ASSUME_OCI_BIN OCI_ASSUME_SHELL OCI_ASSUME_EVAL ZDOTDIR\n'
  } >"$ENVF"
}

step_install() {
  case "$E2E_CHANNEL" in
    brew)
      brew install --cask 123yogin/tap/oci-assume || { fail "brew install --cask 123yogin/tap/oci-assume"; return; }
      ok "brew install --cask 123yogin/tap/oci-assume"
      ;;
    *)
      local args="--version $E2E_VERSION" log="$E2E_WORK/install.log"
      local url="https://raw.githubusercontent.com/123yogin/oci-assume/$E2E_VERSION/install.sh"
      if [ -n "${E2E_INSTALL_SH:-}" ]; then
        grep -q -- '--yes' "$E2E_INSTALL_SH" && args="$args --yes"
        # shellcheck disable=SC2086
        OCI_ASSUME_BASE_URL=${E2E_BASE_URL:-} sh "$E2E_INSTALL_SH" $args 2>"$log" </dev/null
      else
        # v1.0 installers reject --yes; every later one takes it.
        curl -fsSL "$url" | grep -q -- '--yes' && args="$args --yes"
        echo "+ curl -fsSL $url | sh -s -- $args"
        # shellcheck disable=SC2086
        curl -fsSL "$url" | sh -s -- $args 2>"$log"
      fi
      local rc=$?
      cat "$log"
      [ "$rc" = 0 ] && ok "install.sh $args" || fail "install.sh $args exited $rc"
      check "binary at $E2E_BIN_DIR/oci-assume" test -x "$E2E_BIN_DIR/oci-assume"
      if has_setup; then
        check "no TTY: installer skipped the setup handoff" grep -q 'Next: oci-assume setup' "$log"
      fi
      ;;
  esac
}

step_setup() {
  need_setup "setup" || return 0
  local sh extra=""
  sched_allowed || extra=--no-background
  for sh in $E2E_SHELLS; do
    # shellcheck disable=SC2086
    if OA setup --yes --no-oci-cli --no-login --shell "$sh" $extra </dev/null; then
      ok "setup --yes --no-oci-cli --no-login --shell $sh $extra"
    else
      fail "setup --shell $sh exited $?"
    fi
  done
}

# path_line is the line setup writes when the install dir is not on PATH.
path_line() {
  local d=$E2E_BIN_DIR
  case "$d" in "$HOME"/*) d='$HOME'${d#"$HOME"} ;; esac
  printf 'export PATH="%s:$PATH"\n' "$d"
}

rc_for() { case "$1" in zsh) echo "$HOME/.zshrc" ;; bash) echo "$HOME/.bashrc" ;; esac; }

sched_present() {
  case "$OS" in
    Darwin) launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1 ;;
    *) systemctl --user is-enabled oci-assume.timer >/dev/null 2>&1 || crontab -l 2>/dev/null | grep -q '# oci-assume$' ;;
  esac
}

step_assert_setup() {
  need_setup "setup results" || return 0
  local sh f sd
  for sh in $E2E_SHELLS; do
    f=$(rc_for "$sh")
    check "$sh: marked block in $f" grep -qF "$MARK_BEGIN" "$f"
    check "$sh: hook line in $f" grep -qF "oci-assume init $sh" "$f"
    case ":$PATH:" in
      *":$E2E_BIN_DIR:"*) skip "$sh: PATH line ($E2E_BIN_DIR already on PATH)" ;;
      *)
        check "$sh: PATH line '$(path_line)' in $f" grep -qF "$(path_line)" "$f"
        ;;
    esac
  done
  if [ "$OS" = Darwin ] && [[ " $E2E_SHELLS " == *" bash "* ]]; then
    check "bash: login file sources ~/.bashrc" grep -qsE '\.bashrc' "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"
  fi
  sd=$(state_dir)
  check "receipt at $sd/receipt.json" test -s "$sd/receipt.json"
  if ! sched_allowed; then
    skip "scheduler entry (stubbed outside CI)"
  elif [ "$OS" = Darwin ]; then
    check "launchctl print gui/$(id -u)/$LABEL" launchctl print "gui/$(id -u)/$LABEL"
    check "plist in ~/Library/LaunchAgents" test -f "$HOME/Library/LaunchAgents/$LABEL.plist"
  elif systemctl --user show-environment >/dev/null 2>&1; then
    check "systemd timer oci-assume.timer enabled" systemctl --user is-enabled oci-assume.timer
  else
    check "crontab line marked # oci-assume" sh -c "crontab -l | grep -q '# oci-assume$'"
  fi
}

# xmarks <file> <allowed-regex>: fails on any doctor ✗ line the regex does not allow.
xmarks() {
  local line bad=0
  while IFS= read -r line; do
    case "$line" in ✗*) ;; *) continue ;; esac
    if [ -n "$2" ] && printf '%s\n' "$line" | grep -qE "$2"; then
      echo "     expected: $line"
    else
      echo "     unexpected: $line"
      bad=1
    fi
  done <"$1"
  return "$bad"
}

# Expected doctor ✗ lines that are not product failures.
doctor_allowed() {
  local a="^✗ shell hook not loaded"
  [ "${1:-}" = hooked ] && a='^$'
  sched_allowed || a="$a|^✗ daemon not installed"
  oci --version >/dev/null 2>&1 || a="$a|^✗ oci CLI"
  printf '%s\n' "$a"
}

step_shell() {
  need_setup "new interactive shell" || return 0
  local sh out probe
  # shellcheck disable=SC2016
  probe='case "$(type oci-assume 2>&1)" in *function*) echo FN=yes ;; *) echo FN=no ;; esac
oci-assume e2e; echo "RC1=$?"
echo "P1=${OCI_CLI_PROFILE-}"; echo "A1=${OCI_CLI_AUTH-<unset>}"; echo "R1=${OCI_CLI_REGION-}"
echo "PROMPT=$(oci_assume_prompt)"
oci-assume doctor >"$E2E_DOCTOR_OUT" 2>&1; echo "DOC=$?"
oci-assume e2e-key; echo "RC2=$?"
echo "P2=${OCI_CLI_PROFILE-}"; echo "A2=${OCI_CLI_AUTH-<unset>}"'
  for sh in $E2E_SHELLS; do
    command -v "$sh" >/dev/null 2>&1 || { fail "$sh not installed"; continue; }
    out=$(E2E_DOCTOR_OUT="$E2E_WORK/doctor-$sh.txt" "$sh" -i -c "$probe" </dev/null 2>"$E2E_WORK/shell-$sh.err")
    v() { printf '%s\n' "$out" | sed -n "s/^$1=//p" | tail -n 1; }
    echo "--- $sh -i -c"; printf '%s\n' "$out" | sed 's/^/     /'
    [ "$(v FN)" = yes ] && ok "$sh: oci-assume is a shell function" || { fail "$sh: oci-assume is not a shell function"; cat "$E2E_WORK/shell-$sh.err"; }
    [ "$(v RC1)" = 0 ] && ok "$sh: oci-assume e2e exits 0" || fail "$sh: oci-assume e2e exited $(v RC1)"
    [ "$(v P1)" = e2e ] && ok "$sh: OCI_CLI_PROFILE=e2e" || fail "$sh: OCI_CLI_PROFILE=$(v P1)"
    [ "$(v A1)" = security_token ] && ok "$sh: OCI_CLI_AUTH=security_token" || fail "$sh: OCI_CLI_AUTH=$(v A1)"
    [ "$(v R1)" = us-ashburn-1 ] && ok "$sh: OCI_CLI_REGION=us-ashburn-1" || fail "$sh: OCI_CLI_REGION=$(v R1)"
    printf '%s\n' "$(v PROMPT)" | grep -qE '^\[oci e2e [0-9]+m\]$' && ok "$sh: prompt helper prints $(v PROMPT)" || fail "$sh: prompt helper printed '$(v PROMPT)'"
    [ "$(v RC2)" = 0 ] && [ "$(v P2)" = e2e-key ] && ok "$sh: oci-assume e2e-key switches profile" || fail "$sh: e2e-key rc=$(v RC2) profile=$(v P2)"
    [ "$(v RC2)" = 0 ] && [ "$(v A2)" = '<unset>' ] && ok "$sh: e2e-key clears OCI_CLI_AUTH" || fail "$sh: OCI_CLI_AUTH=$(v A2) after e2e-key"
    echo "--- doctor in hooked $sh"; sed 's/^/     /' "$E2E_WORK/doctor-$sh.txt"
    xmarks "$E2E_WORK/doctor-$sh.txt" "$(doctor_allowed hooked)" && ok "$sh: doctor in a hooked shell has no unexpected ✗" || fail "$sh: doctor in a hooked shell has unexpected ✗"
  done
}

expires() { OA status --json | awk '/"profile": "e2e"/{f=1} f && /"expires"/{gsub(/[",]/,"",$2); print $2; exit}'; }

step_commands() {
  local out before after
  out=$(OA status) && ok "oci-assume status exits 0" || fail "oci-assume status"
  printf '%s\n' "$out" | sed 's/^/     /'
  printf '%s\n' "$out" | grep -qE '^e2e +us-ashburn-1 +[0-9]+m' && ok "status lists e2e with its expiry" || fail "status row for e2e"
  printf '%s\n' "$out" | grep -qE '^e2e-key +us-phoenix-1' && ok "status lists e2e-key" || fail "status row for e2e-key"

  OA doctor >"$E2E_WORK/doctor-plain.txt" 2>&1
  echo "--- doctor (no hook in this shell)"; sed 's/^/     /' "$E2E_WORK/doctor-plain.txt"
  if has_setup; then
    check "doctor without a hook reports exactly 'shell hook not loaded'" grep -q '^✗ shell hook not loaded' "$E2E_WORK/doctor-plain.txt"
  fi
  xmarks "$E2E_WORK/doctor-plain.txt" "$(doctor_allowed)" && ok "doctor has no unexpected ✗" || fail "doctor has unexpected ✗"

  before=$(expires)
  sleep 2
  out=$(OA refresh e2e --threshold 24h 2>&1) && ok "refresh e2e: $out" || fail "refresh e2e: $out"
  after=$(expires)
  [ -n "$before" ] && [[ "$after" > "$before" ]] && ok "token expiry moved $before -> $after" || fail "token expiry did not move ($before -> $after)"

  out=$(OA version)
  case "$out" in
    "oci-assume ${E2E_VERSION#v} "* | "oci-assume $E2E_VERSION "*) ok "version: $out" ;;
    *) fail "version '$out' is not $E2E_VERSION" ;;
  esac
}

# bg_result prints ok when state.json holds a successful background result for e2e.
bg_result() {
  awk '/"last_result"/{lr=1} lr && /^    "e2e": \{/{e=1} e && /"background": true/{bg=1} e && /"ok": true/{good=1} e && /^    \}/{e=0}
       END{if (bg && good) print "ok"}' "$(state_dir)/state.json" 2>/dev/null
}

step_background() {
  need_setup "background job" || return 0
  sched_allowed || { skip "background job (stubbed outside CI; E2E_ALLOW_LAUNCHD=1 runs it)"; return 0; }
  local i cmd
  case "$OS" in
    Darwin) launchctl kickstart -k "gui/$(id -u)/$LABEL" && ok "launchctl kickstart" || fail "launchctl kickstart" ;;
    *)
      if systemctl --user show-environment >/dev/null 2>&1; then
        systemctl --user start oci-assume.service && ok "systemctl --user start oci-assume.service" || fail "systemctl start"
      else
        cmd=$(crontab -l | grep '# oci-assume$' | awk '{ $1=$2=$3=$4=$5=""; print }' | sed 's/# oci-assume$//')
        sh -c "$cmd" && ok "ran the cron line" || fail "cron line: $cmd"
      fi
      ;;
  esac
  for i in $(seq 1 60); do
    [ "$(bg_result)" = ok ] && break
    sleep 1
  done
  [ "$(bg_result)" = ok ] && ok "state.json has a background result for e2e after ${i}s" || { fail "no background result for e2e in 60s"; cat "$(state_dir)/state.json"; }
}

rc_identical() {
  local f s ok_all=0
  for f in $RC_FILES; do
    s="$E2E_WORK/snap/${f//\//_}"
    if [ -e "$s.absent" ]; then
      [ ! -e "$HOME/$f" ] || { echo "     $f exists but did not before"; ok_all=1; }
    elif ! cmp -s "$s" "$HOME/$f"; then
      echo "     $f differs from the snapshot"; diff "$s" "$HOME/$f" | sed 's/^/       /'; ok_all=1
    fi
  done
  return "$ok_all"
}

step_uninstall() {
  need_setup "uninstall" || return 0
  local sh
  OA uninstall && ok "oci-assume uninstall" || fail "oci-assume uninstall exited $?"
  rc_identical && ok "rc files byte-identical to the snapshot (or absent)" || fail "rc files changed by setup+uninstall"
  if sched_allowed; then
    sched_present && fail "scheduler entry still present" || ok "scheduler entry gone"
  fi
  check "launchd plist / units gone" sh -c "! ls '$HOME/Library/LaunchAgents/$LABEL.plist' '$HOME/.config/systemd/user/oci-assume.timer' 2>/dev/null | grep -q ."
  check "receipt gone" test ! -e "$(state_dir)/receipt.json"
  rm -f "$E2E_BIN_DIR/oci-assume" && ok "removed $E2E_BIN_DIR/oci-assume"
  local pl f left=""
  pl=$(path_line)
  for f in $RC_FILES; do grep -qsF "$pl" "$HOME/$f" && left="$left $f"; done
  [ -z "$left" ] && ok "PATH line '$pl' gone from rc files" || fail "PATH line left in:$left"
  for sh in $E2E_SHELLS; do
    check "$sh: new shell no longer finds oci-assume" "$sh" -i -c '! command -v oci-assume' </dev/null
  done
}

step_zap() {
  brew uninstall --zap --cask oci-assume && ok "brew uninstall --zap --cask oci-assume" || fail "brew uninstall --zap"
  launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1 && fail "launchd agent still loaded after zap" || ok "no launchd agent after zap"
}

run_step() {
  # shellcheck source=/dev/null
  [ -f "$ENVF" ] && . "$ENVF"
  case "$1" in
    prepare) step_prepare ;;
    install) step_install ;;
    setup) step_setup ;;
    assert-setup) step_assert_setup ;;
    shell) step_shell ;;
    commands) step_commands ;;
    background) step_background ;;
    uninstall) step_uninstall ;;
    zap) step_zap ;;
    all) for s in prepare install setup assert-setup shell commands background uninstall; do echo "=== $s"; run_step "$s"; done ;;
    *) die "unknown step: $1" ;;
  esac
}

[ $# -gt 0 ] || die "usage: e2e.sh <step>... | all"
for s in "$@"; do run_step "$s"; done
[ "$FAILS" -eq 0 ] || { echo "$FAILS check(s) failed"; exit 1; }
