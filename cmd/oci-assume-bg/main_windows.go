//go:build windows

// Command oci-assume-bg is oci-assume built with -H=windowsgui, so the scheduled task opens no console.
package main

import (
	"os"

	"github.com/123yogin/oci-assume/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
