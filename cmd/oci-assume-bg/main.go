//go:build windows

package main

import (
	"os"

	"github.com/123yogin/oci-assume/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
