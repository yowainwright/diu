//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIScanRefreshesZshExecutableMap(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	if err := os.Remove(filepath.Join(f.managed, "probe")); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, filepath.Join(f.managed, "extra"), "#!/bin/sh\nexit 0\n", 0o700)
	assertCLIExit(t, f.cli(t, "scan"), 0)

	integration := readCLIFile(t, filepath.Join(f.config.Daemon.DataDir, "shell-integration.zsh"))
	if strings.Contains(integration, "_DIU_TOOL_MAP+=('probe'") {
		t.Fatalf("removed executable remains in listener map: %s", integration)
	}
	if !strings.Contains(integration, "_DIU_TOOL_MAP+=('extra'") {
		t.Fatalf("new executable is absent from listener map: %s", integration)
	}
}
