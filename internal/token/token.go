// Package token reads the expiry of an OCI session token (a JWT).
package token

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"
)

// ErrInsecurePerms means a token or key file is accessible by group or others.
var ErrInsecurePerms = errors.New("file is readable by group or others")

// Info is the parsed expiry; Valid=false means treat as expired.
type Info struct {
	Expiry time.Time
	Valid  bool
}

// Read parses the token at path. Only a permission refusal is an error.
func Read(path string, now time.Time) (Info, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return Info{}, nil
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return Info{}, fmt.Errorf("%w: %s; fix: chmod 600 %s", ErrInsecurePerms, path, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Info{}, nil
	}
	parts := bytes.Split(bytes.TrimSpace(b), []byte("."))
	if len(parts) != 3 {
		return Info{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(string(parts[1]))
	if err != nil {
		return Info{}, nil
	}
	var claims struct {
		Exp *float64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp == nil {
		return Info{}, nil
	}
	if e := *claims.Exp; math.IsNaN(e) || math.IsInf(e, 0) || e <= 0 || e > 1<<53 {
		return Info{}, nil
	}
	return Info{Expiry: time.Unix(int64(*claims.Exp), 0), Valid: true}, nil
}

// Remaining is time until expiry; <=0 when expired or invalid.
func (i Info) Remaining(now time.Time) time.Duration {
	if !i.Valid {
		return 0
	}
	return i.Expiry.Sub(now)
}
