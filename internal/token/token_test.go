package token

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mk(payload string) string {
	e := base64.RawURLEncoding.EncodeToString
	return e([]byte(`{"alg":"none"}`)) + "." + e([]byte(payload)) + ".sig"
}

func write(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestValidJWT(t *testing.T) {
	now := time.Now()
	p := write(t, mk(`{"exp":`+strconv.FormatInt(now.Add(3000*time.Second).Unix(), 10)+`}`), 0o600)
	info, err := Read(p, now)
	if err != nil || !info.Valid {
		t.Fatalf("got %+v, %v", info, err)
	}
	d := info.Remaining(now)
	if d < 49*time.Minute || d > 51*time.Minute {
		t.Fatalf("remaining %v", d)
	}
}

func TestMalformedTokenIsExpired(t *testing.T) {
	now := time.Now()
	for _, c := range []string{"", "abc", "a.b", "a.!!!.c", mk(`{}`), mk(`{"exp":"x"}`), mk(`{"exp":-1}`), mk(`{"exp":0}`), mk(`{"exp":1e30}`)} {
		info, err := Read(write(t, c, 0o600), now)
		if err != nil || info.Valid || info.Remaining(now) > 0 {
			t.Errorf("%q: got %+v, %v", c, info, err)
		}
	}
}

func TestMissingFileIsExpired(t *testing.T) {
	now := time.Now()
	info, err := Read(filepath.Join(t.TempDir(), "nope"), now)
	if err != nil || info.Valid || info.Remaining(now) > 0 {
		t.Fatalf("got %+v, %v", info, err)
	}
}

func TestInsecurePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes")
	}
	now := time.Now()
	for _, m := range []os.FileMode{0o644, 0o604} {
		p := write(t, mk(`{"exp":9999999999}`), m)
		if _, err := Read(p, now); !errors.Is(err, ErrInsecurePerms) || !strings.Contains(err.Error(), "fix: chmod 600 "+p) {
			t.Fatalf("mode %o: err = %v", m, err)
		}
	}
}

func TestTrailingNewline(t *testing.T) {
	now := time.Now()
	p := write(t, mk(`{"exp":9999999999}`)+"\n", 0o600)
	info, err := Read(p, now)
	if err != nil || !info.Valid {
		t.Fatalf("got %+v, %v", info, err)
	}
}
