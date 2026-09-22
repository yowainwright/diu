//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	if !isE2EContainer() {
		fmt.Fprintln(os.Stderr, "Refusing host execution. Run mise run test-e2e for the isolated Docker suite.")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func isE2EContainer() bool {
	_, markerErr := os.Stat("/.diu-e2e-container")
	_, dockerErr := os.Stat("/.dockerenv")
	linux := runtime.GOOS == "linux"
	nonRoot := os.Getuid() != 0
	optedIn := os.Getenv("DIU_E2E_CONTAINER") == "1"
	return linux && nonRoot && optedIn && markerErr == nil && dockerErr == nil
}
