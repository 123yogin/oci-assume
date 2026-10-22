//go:build !windows

// Command oci-assume-bg is Windows-only; elsewhere it is an empty stub so `go build ./...` passes.
package main

func main() {}
