package shell

import (
	"fmt"
	"regexp"
	"strings"
)

var pwshQuoteRe = strings.NewReplacer("'", "''", "\u2018", "\u2018\u2018", "\u2019", "\u2019\u2019", "\u201a", "\u201a\u201a", "\u201b", "\u201b\u201b")

var regionRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// Validate rejects values that must never reach a shell, even quoted.
func Validate(e Env) error {
	for _, r := range e.Profile {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("profile name contains a control character")
		}
	}
	if e.Region != "" && !regionRe.MatchString(e.Region) {
		return fmt.Errorf("invalid region %q", e.Region)
	}
	return nil
}

// Env is the set of OCI variables to export.
type Env struct {
	Profile, Region string
	Session         bool
}

func quote(sh Shell, v string) string {
	switch sh {
	case Pwsh:
		// PowerShell also treats curly single quotes as delimiters.
		return "'" + pwshQuoteRe.Replace(v) + "'"
	case Fish:
		v = strings.ReplaceAll(v, `\`, `\\`)
		return "'" + strings.ReplaceAll(v, "'", `\'`) + "'"
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func setLine(sh Shell, k, v string) string {
	q := quote(sh, v)
	switch sh {
	case Fish:
		return "set -gx " + k + " " + q + "\n"
	case Pwsh:
		return "$env:" + k + " = " + q + "\n"
	}
	return "export " + k + "=" + q + "\n"
}

// Export renders set lines for e; variables it does not set are unset so a
// previous profile's auth or region cannot leak into this one.
func Export(sh Shell, e Env) string {
	s := setLine(sh, "OCI_CLI_PROFILE", e.Profile)
	if e.Session {
		s += setLine(sh, "OCI_CLI_AUTH", "security_token")
	} else {
		s += unsetLine(sh, "OCI_CLI_AUTH")
	}
	if e.Region != "" {
		s += setLine(sh, "OCI_CLI_REGION", e.Region)
	} else {
		s += unsetLine(sh, "OCI_CLI_REGION")
	}
	return s
}

func unsetLine(sh Shell, k string) string {
	switch sh {
	case Fish:
		return "set -e " + k + "; or true\n"
	case Pwsh:
		return "Remove-Item Env:" + k + " -ErrorAction SilentlyContinue\n"
	}
	return "unset " + k + "\n"
}

// Unset renders lines that clear the variables Export sets.
func Unset(sh Shell) string {
	var b strings.Builder
	for _, v := range []string{"OCI_CLI_PROFILE", "OCI_CLI_AUTH", "OCI_CLI_REGION"} {
		b.WriteString(unsetLine(sh, v))
	}
	return b.String()
}
