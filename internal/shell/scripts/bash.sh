# oci-assume shell hook (bash). Add to ~/.bashrc: eval "$(oci-assume init bash)"
export OCI_ASSUME_SHELL=bash

oci-assume() {
  case "${1:-}" in
    status|refresh|login|prompt|daemon|init|doctor|setup|uninstall|version|help|completion|__complete|__completeNoDesc|--help|-h|--version)
      command oci-assume "$@"
      return $?
      ;;
  esac
  local out rc
  out="$(OCI_ASSUME_EVAL=1 command oci-assume "$@")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    return "$rc"
  fi
  eval "$out"
}

oci_assume_prompt() {
  local p
  p="$(command oci-assume prompt 2>/dev/null)"
  if [ -n "$p" ]; then
    printf '[oci %s]' "$p"
  fi
  return 0
}
