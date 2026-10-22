# oci-assume shell hook (fish). Add to config.fish: oci-assume init fish | source
set -gx OCI_ASSUME_SHELL fish

function oci-assume
    switch "$argv[1]"
        case status refresh login prompt daemon init doctor setup uninstall version help completion __complete __completeNoDesc --help -h --version
            command oci-assume $argv
            return $status
    end
    set -l out (OCI_ASSUME_EVAL=1 command oci-assume $argv)
    set -l rc $status
    if test $rc -ne 0
        return $rc
    end
    printf '%s\n' $out | source
    return 0
end

function oci_assume_prompt
    set -l p (command oci-assume prompt 2>/dev/null)
    if test -n "$p"
        printf '[oci %s]' "$p"
    end
    return 0
end
