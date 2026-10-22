// Command oci is the fake oci CLI for Go tests on every OS; it mirrors the sh fake in ../../oci.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	mode := os.Getenv("FAKE_OCI_MODE")
	if mode == "" {
		mode = "ok"
	}
	args := os.Args[1:]
	if log := os.Getenv("FAKE_OCI_LOG"); log != "" {
		appendFile(log, strings.Join(args, " ")+"\n")
		var env strings.Builder
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "OCI_CLI_") {
				env.WriteString(kv + "\n")
			}
		}
		appendFile(log+".env", env.String())
	}
	if mode == "slow" {
		time.Sleep(30 * time.Second)
		return
	}
	sub := ""
	if len(args) >= 2 {
		sub = args[0] + " " + args[1]
	}
	switch sub {
	case "session validate":
		if mode == "expired" {
			fail(1, "Error: session token has expired")
		}
		return
	case "session refresh":
		switch mode {
		case "expired":
			fail(1, "Error: session token has expired")
		case "refresh-fail":
			fail(1, "Error: session token is not valid or has expired")
		case "refresh-error":
			fail(1, "ServiceError: status 500, InternalServerError")
		}
		writeToken()
		return
	case "session authenticate":
		if mode == "auth-fail" {
			fail(1, "Error: authentication failed")
		}
		if mode == "auth-ok" {
			fmt.Println("FAKE-AUTH-STDOUT")
		}
		writeToken()
		return
	}
	fail(2, "fake oci: unsupported args: "+strings.Join(args, " "))
}

func fail(code int, msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(code)
}

func appendFile(path, s string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fail(3, err.Error())
	}
	_, err = f.WriteString(s)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fail(3, err.Error())
	}
}

// writeToken writes an unsigned JWT that expires in an hour.
func writeToken() {
	p := os.Getenv("FAKE_TOKEN_PATH")
	if p == "" {
		return
	}
	b64 := base64.RawURLEncoding.EncodeToString
	tok := b64([]byte(`{"alg":"none"}`)) + "." + b64(fmt.Appendf(nil, `{"exp":%d}`, time.Now().Add(time.Hour).Unix())) + ".sig"
	if err := os.WriteFile(p, []byte(tok), 0o600); err != nil {
		fail(3, err.Error())
	}
}
