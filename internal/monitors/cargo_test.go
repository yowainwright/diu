package monitors

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestInitializeCargoRejectsMissingCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := NewCargoMonitor().Initialize(core.DefaultConfig()); err == nil {
		t.Fatal("missing Cargo was accepted")
	}
}

func TestParseCommandCargoPreservesArguments(t *testing.T) {
	monitor := NewCargoMonitor()
	record, err := monitor.ParseCommand("cargo", []string{"install", "ripgrep"})
	if err != nil {
		t.Fatal(err)
	}
	hasIdentity := record.Tool == core.ToolCargo && record.Command == "cargo"
	hasArguments := slices.Equal(record.Args, []string{"install", "ripgrep"})
	validRecord := hasIdentity && hasArguments && len(record.PackagesAffected) == 0
	if !validRecord {
		t.Fatalf("submitted install inferred package changes: %#v", record)
	}
}

func TestGetInstalledPackagesCargo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CARGO_INSTALL_ROOT", root)
	prependFakeCommand(t, "cargo", "#!/bin/sh\nprintf 'ripgrep v14.1.1:\\n    rg\\ncli-tools v1.2.3 (/source/tools):\\n    tool-one\\n    tool-two\\n'\n")
	monitor, err := NewMonitor("cargo")
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Initialize(core.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	assertCargoPackages(t, packages, root)
}

func assertCargoPackages(t *testing.T, packages []*core.PackageInfo, root string) {
	t.Helper()
	if len(packages) != 2 {
		t.Fatalf("packages = %#v, want two crates", packages)
	}
	want := map[string]string{"cli-tools": "1.2.3", "ripgrep": "14.1.1"}
	for _, pkg := range packages {
		validPackage := pkg.Tool == "cargo" && pkg.Version == want[pkg.Name]
		if !validPackage {
			t.Fatalf("unexpected package: %#v", pkg)
		}
	}
	if packages[0].Path != filepath.Join(root, "bin", "rg") {
		t.Fatalf("ripgrep path = %q", packages[0].Path)
	}
}

func TestCargoExecutablesResolvesInstallRoots(t *testing.T) {
	for _, kind := range []string{"default", "cargo_home", "config"} {
		t.Run(kind, func(t *testing.T) {
			root := cargoRootFixture(t, kind)
			binaries, err := CargoExecutables("")
			if err != nil {
				t.Fatal(err)
			}
			if binaries["rg"].Path != filepath.Join(root, "bin", "rg") {
				t.Fatalf("binary = %#v, want root %q", binaries["rg"], root)
			}
		})
	}
}

func cargoRootFixture(t *testing.T, kind string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CARGO_HOME", "")
	t.Setenv("CARGO_INSTALL_ROOT", "")
	root := filepath.Join(home, ".cargo")
	root = cargoFixtureRoot(t, kind, root)
	if err := os.MkdirAll(root, core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	writeFakeCommand(t, root, ".crates.toml", "[v1]\n")
	prependFakeCommand(t, "cargo", "#!/bin/sh\nprintf 'ripgrep v14.1.1:\\n    rg\\n'\n")
	return root
}

func cargoFixtureRoot(t *testing.T, kind, root string) string {
	t.Helper()
	if kind != "default" {
		root = t.TempDir()
		t.Setenv("CARGO_HOME", root)
	}
	if kind == "config" {
		root = t.TempDir()
		t.Setenv("PATH", filepath.Join(root, "bin")+":"+os.Getenv("PATH"))
	}
	return root
}

func TestCargoExecutablesKeepsInventoryFromFirstRoot(t *testing.T) {
	first := cargoRootFixture(t, "config")
	second := t.TempDir()
	writeFakeCommand(t, second, ".crates.toml", "[v1]\n")
	t.Setenv("CARGO_HOME", second)
	t.Setenv("DIU_FIRST_ROOT", first)
	prependFakeCommand(t, "cargo", cargoMultipleRootsScript)
	binaries, err := CargoExecutables("")
	if err != nil {
		t.Fatal(err)
	}
	assertCargoFirstRoot(t, binaries, first)
}

func assertCargoFirstRoot(t *testing.T, binaries map[string]*core.PackageInfo, root string) {
	t.Helper()
	for _, name := range []string{"rg", "first-only"} {
		pkg := binaries[name]
		valid := pkg != nil && pkg.Path == filepath.Join(root, "bin", name)
		if !valid {
			t.Fatalf("%s = %#v, want first-root inventory", name, pkg)
		}
	}
	if binaries["second-only"] != nil {
		t.Fatal("merged binaries from another installation of the same crate")
	}
}

const cargoMultipleRootsScript = `#!/bin/sh
printf 'ripgrep v14.1.1:\n    rg\n'
if [ "$CARGO_INSTALL_ROOT" = "$DIU_FIRST_ROOT" ]; then
    printf '    first-only\n'
else
    printf '    second-only\n'
fi
`

func TestGetInstalledPackagesCargoRejectsMalformedInventory(t *testing.T) {
	for _, output := range []string{
		"garbage", "    orphan\n", "crate v1.0.0:\n", "crate v1.0.0\n    bin\n",
		"crate vbanana:\n    bin\n", "crate v1.0.0:\n    ../bin\n",
		"crate v1.0.0:\n    bin\n    bin\n",
	} {
		t.Run(strings.ReplaceAll(output, "\n", "_"), func(t *testing.T) {
			t.Setenv("DIU_CARGO_OUTPUT", output)
			prependFakeCommand(t, "cargo", "#!/bin/sh\nprintf '%s' \"$DIU_CARGO_OUTPUT\"\n")
			if _, err := NewCargoMonitor().GetInstalledPackages(); err == nil {
				t.Fatal("malformed inventory was accepted")
			}
		})
	}
}

func TestGetInstalledPackagesCargoHandlesEmptyAndFailedInventory(t *testing.T) {
	prependFakeCommand(t, "cargo", "#!/bin/sh\nexit 0\n")
	packages, err := NewCargoMonitor().GetInstalledPackages()
	hasEmptyInventory := err == nil && len(packages) == 0
	if !hasEmptyInventory {
		t.Fatalf("empty inventory = %v, %v", packages, err)
	}
	prependFakeCommand(t, "cargo", "#!/bin/sh\nprintf 'inventory failed' >&2\nexit 1\n")
	_, err = NewCargoMonitor().GetInstalledPackages()
	hasErrorDetail := err != nil && strings.Contains(err.Error(), "inventory failed")
	if !hasErrorDetail {
		t.Fatalf("missing Cargo error detail: %v", err)
	}
}

func TestCargoExecutablesKeepsRootOutOfArguments(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root ; $(touch injected)")
	t.Setenv("DIU_EXPECTED_CARGO_ROOT", root)
	prependFakeCommand(t, "cargo", `#!/bin/sh
[ "$#" -eq 2 ] && [ "$1" = install ] && [ "$2" = --list ] || exit 1
[ "$CARGO_INSTALL_ROOT" = "$DIU_EXPECTED_CARGO_ROOT" ] || exit 2
printf 'ripgrep v14.1.1:\n    rg\n'
`)
	binaries, err := CargoExecutables(root)
	if err != nil {
		t.Fatal(err)
	}
	if binaries["rg"].Path != filepath.Join(root, "bin", "rg") {
		t.Fatalf("unexpected root: %#v", binaries["rg"])
	}
}

func TestCargoExecutablesRejectsReceiptOutsideRoot(t *testing.T) {
	root := cargoRootFixture(t, "config")
	outside := filepath.Join(t.TempDir(), "receipt")
	writeFakeCommand(t, filepath.Dir(outside), filepath.Base(outside), "[v1]\n")
	if err := os.Remove(filepath.Join(root, ".crates.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".crates.toml")); err != nil {
		t.Fatal(err)
	}
	binaries, err := CargoExecutables("")
	if err != nil {
		t.Fatal(err)
	}
	if binaries["rg"].Path != "" {
		t.Fatalf("escaped receipt accepted: %#v", binaries["rg"])
	}
}
