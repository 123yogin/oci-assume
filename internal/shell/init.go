// Package shell renders shell init scripts, env export lines and prompt text.
package shell

import (
	"embed"
	"fmt"
)

//go:embed scripts/*
var scripts embed.FS

// Shell is a supported shell name.
type Shell string

const (
	Zsh  Shell = "zsh"
	Bash Shell = "bash"
	Fish Shell = "fish"
	Pwsh Shell = "pwsh"
)

// Parse validates a shell name.
func Parse(s string) (Shell, error) {
	switch Shell(s) {
	case Zsh, Bash, Fish, Pwsh:
		return Shell(s), nil
	}
	return "", fmt.Errorf("unsupported shell %q (want zsh, bash, fish or pwsh)", s)
}

var scriptFile = map[Shell]string{
	Zsh: "scripts/zsh.sh", Bash: "scripts/bash.sh",
	Fish: "scripts/fish.fish", Pwsh: "scripts/pwsh.ps1",
}

// Init returns the static hook script for sh.
func Init(sh Shell) string {
	f, ok := scriptFile[sh]
	if !ok {
		return ""
	}
	b, err := scripts.ReadFile(f)
	if err != nil {
		return ""
	}
	return string(b)
}
