package monitors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestGetInstalledPackagesKrewUsesLocalActiveVersions(t *testing.T) {
	root := krewInventoryFixture(t)
	monitor, err := NewMonitor("krew")
	if err != nil {
		t.Fatal(err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 2 {
		t.Fatalf("packages = %#v, want two installed plugins", packages)
	}
	for _, pkg := range packages {
		assertKrewPackage(t, root, pkg)
	}
}

func assertKrewPackage(t *testing.T, root string, pkg *core.PackageInfo) {
	t.Helper()
	want := filepath.Join(root, "store", pkg.Name, "v1.2.3")
	hasIdentity := pkg.Tool == "krew" && pkg.Version == "v1.2.3"
	valid := hasIdentity && pkg.Path == want
	if !valid {
		t.Fatalf("unexpected Krew package: %#v", pkg)
	}
}

func krewInventoryFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "krew root")
	t.Setenv("KREW_ROOT", root)
	for _, name := range []string{"ctx", "view-secret"} {
		krewPluginFixture(t, root, name)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestKrewExecutablesRequiresInstalledTargets(t *testing.T) {
	root := krewInventoryFixture(t)
	link := filepath.Join(root, "bin", "kubectl-ctx")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	binaries, err := KrewExecutables()
	if err != nil {
		t.Fatal(err)
	}
	if len(binaries) != 1 {
		t.Fatalf("executables = %#v, want only the healthy plugin", binaries)
	}
	pkg := binaries["kubectl-view_secret"]
	hasIdentity := pkg != nil && pkg.Name == "view-secret"
	if !hasIdentity {
		t.Fatalf("hyphenated plugin identity lost: %#v", binaries)
	}
	assertKrewMissingBinary(t)
}

func assertKrewMissingBinary(t *testing.T) {
	t.Helper()
	packages, err := NewKrewMonitor().GetInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	missing := len(packages) == 2 && packages[0].Path == ""
	if !missing {
		t.Fatalf("missing binary must retain an unlocated install: %#v, %v", packages, err)
	}
}

func TestKrewExecutablesRejectsTargetOutsideStore(t *testing.T) {
	root := krewInventoryFixture(t)
	link := filepath.Join(root, "bin", "kubectl-ctx")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", link); err != nil {
		t.Fatal(err)
	}
	binaries, err := KrewExecutables()
	if err != nil {
		t.Fatal(err)
	}
	if binaries["kubectl-ctx"] != nil {
		t.Fatal("claimed an executable outside the installed plugin store")
	}
}

func TestGetInstalledPackagesKrewReportsInventoryFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KREW_ROOT", root)
	if err := os.WriteFile(filepath.Join(root, "receipts"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKrewMonitor().GetInstalledPackages(); err == nil {
		t.Fatal("invalid receipts directory reported a successful empty scan")
	}
}

func TestGetInstalledPackagesKrewDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KREW_ROOT", "")
	krewPluginFixture(t, filepath.Join(home, ".krew"), "ctx")
	packages, err := NewKrewMonitor().GetInstalledPackages()
	valid := err == nil && len(packages) == 1
	if !valid {
		t.Fatalf("default Krew inventory = %#v, %v", packages, err)
	}
}

func TestGetInstalledPackagesKrewAcceptsNativePluginNames(t *testing.T) {
	root := krewInventoryFixture(t)
	for _, name := range []string{"custom_tool", "Custom"} {
		krewPluginFixture(t, root, name)
	}
	packages, err := NewKrewMonitor().GetInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 4 {
		t.Fatalf("inventory = %#v, want all native Krew names", packages)
	}
}

func krewPluginFixture(t *testing.T, root, name string) {
	t.Helper()
	for _, dir := range []string{"receipts", "bin", filepath.Join("store", name, "v1.2.3")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "receipts", name+".yaml")
	if err := os.WriteFile(path, []byte("kind: Plugin\n"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "store", name, "v1.2.3", "plugin")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := "kubectl-" + strings.ReplaceAll(name, "-", "_")
	if err := os.Symlink(bin, filepath.Join(root, "bin", link)); err != nil {
		t.Fatal(err)
	}
}
