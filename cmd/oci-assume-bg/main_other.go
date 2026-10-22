//go:build !windows

package main

// The helper is Windows-only; this stub keeps `go build ./...` working elsewhere.
func main() {}
