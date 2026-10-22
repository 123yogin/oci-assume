package cli

import (
	"os"
	"testing"

	"github.com/123yogin/oci-assume/internal/testutil/fakeoci"
)

func TestMain(m *testing.M) {
	code := m.Run()
	fakeoci.Cleanup()
	os.Exit(code)
}
