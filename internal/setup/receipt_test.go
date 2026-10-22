package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestReceiptRoundTripAtomic0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	r, err := LoadReceipt(dir)
	if err != nil || !reflect.DeepEqual(r, Receipt{}) {
		t.Fatalf("missing receipt: %+v %v", r, err)
	}
	want := Receipt{
		Version:    "1",
		Files:      []FileEntry{{Path: "/h/.zshrc", Kind: "block"}, {Path: "/h/.config/fish/conf.d/oci-assume.fish", Kind: "dropin"}},
		Background: &BackgroundEntry{Backend: "launchd", Consented: true},
		OCICLI:     "brew",
		LastHeal:   time.Date(2026, 10, 22, 1, 2, 3, 0, time.UTC),
	}
	if err := SaveReceipt(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadReceipt(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v %v", got, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp files left: %v", ents)
	}
}

func TestReceiptCorruptIsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "receipt.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReceipt(dir); err == nil {
		t.Fatal("want parse error")
	}
}
