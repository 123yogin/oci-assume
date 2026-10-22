package ociconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, name string) []Profile {
	t.Helper()
	ps, err := Load(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestLoadOrder(t *testing.T) {
	ps := load(t, "order.ini")
	want := []string{"DEFAULT", "dev", "prod-admin"}
	if len(ps) != len(want) {
		t.Fatalf("got %d profiles, want %d", len(ps), len(want))
	}
	for i, w := range want {
		if ps[i].Name != w {
			t.Errorf("ps[%d]=%q want %q", i, ps[i].Name, w)
		}
	}
}

func TestDefaultInheritance(t *testing.T) {
	ps := load(t, "order.ini")
	dev, ok := Find(ps, "dev")
	if !ok || dev.Region != "us-ashburn-1" || dev.Tenancy != "ocid1.tenancy.oc1..t" {
		t.Fatalf("dev did not inherit DEFAULT: %+v", dev)
	}
	prod, _ := Find(ps, "prod-admin")
	if prod.Region != "eu-frankfurt-1" {
		t.Errorf("override lost: %q", prod.Region)
	}
	if !dev.IsSession() || (Profile{}).IsSession() {
		t.Error("IsSession wrong")
	}
	if _, ok := Find(ps, "DEV"); ok {
		t.Error("Find must be case-sensitive")
	}
}

func TestTildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dev, _ := Find(load(t, "tilde.ini"), "dev")
	want := filepath.Join(home, ".oci/sessions/dev/token")
	if dev.SecurityTokenFile != want {
		t.Errorf("got %q want %q", dev.SecurityTokenFile, want)
	}
	if !filepath.IsAbs(dev.KeyFile) {
		t.Errorf("key_file not absolute: %q", dev.KeyFile)
	}
}

func TestRelativePathResolvedAgainstConfigDir(t *testing.T) {
	path := filepath.Join("testdata", "relative.ini")
	ps, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := Find(ps, "dev")
	want := filepath.Join(filepath.Dir(path), "keys/k.pem")
	if dev.KeyFile != want {
		t.Errorf("got %q want %q", dev.KeyFile, want)
	}
}

func TestCommentsAndWhitespace(t *testing.T) {
	ps := load(t, "comments.ini")
	dev, ok := Find(ps, "dev")
	if !ok {
		t.Fatal("dev missing")
	}
	if dev.KeyFile != "/k/dev.pem" || dev.SecurityTokenFile != "/t/dev" || dev.Region != "us-phoenix-1" {
		t.Errorf("bad parse: %+v", dev)
	}
}

func TestMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}

func TestDefaultPathEnv(t *testing.T) {
	t.Setenv("OCI_CLI_CONFIG_FILE", "/x/y/config")
	if got := DefaultPath(); got != "/x/y/config" {
		t.Errorf("got %q", got)
	}
	t.Setenv("OCI_CLI_CONFIG_FILE", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := DefaultPath(); got != filepath.Join(home, ".oci", "config") {
		t.Errorf("got %q", got)
	}
}
