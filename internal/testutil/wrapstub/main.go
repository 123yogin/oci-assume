// Command wrapstub stands in for oci-assume in the shell wrapper tests, on every OS.
// STUB_OUT is printed as a line; STUB_LOG gets "<arg1>=<OCI_ASSUME_EVAL or unset>";
// STUB_EXIT is the exit code for any first argument but "status".
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	if log := os.Getenv("STUB_LOG"); log != "" {
		v := os.Getenv("OCI_ASSUME_EVAL")
		if v == "" {
			v = "unset"
		}
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(3)
		}
		_, err = fmt.Fprintf(f, "%s=%s\n", arg, v)
		if cerr := f.Close(); err != nil || cerr != nil {
			os.Exit(3)
		}
	}
	if out, ok := os.LookupEnv("STUB_OUT"); ok {
		fmt.Println(out)
	}
	if code, err := strconv.Atoi(os.Getenv("STUB_EXIT")); err == nil && arg != "status" {
		os.Exit(code)
	}
}
