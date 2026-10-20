package monitors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestGetInstalledPackagesPipxPreservesInstallIdentities(t *testing.T) {
	root := pipxInventoryFixture(t)
	monitor, err := NewMonitor("pipx")
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
	assertPipxInstallIdentities(t, packages, root)
}

func assertPipxInstallIdentities(t *testing.T, packages []*core.PackageInfo, root string) {
	t.Helper()
	if len(packages) != 2 {
		t.Fatalf("packages = %#v, want two pipx installs", packages)
	}
	for index, name := range []string{"ruff", "ruff@dev"} {
		pkg := packages[index]
		hasIdentity := pkg.Name == name && pkg.Tool == "pipx"
		valid := hasIdentity && pkg.Version == "0.12.0"
		if !valid {
			t.Fatalf("unexpected pipx identity: %#v", pkg)
		}
		if pkg.Path != filepath.Join(root, "venvs", name, "bin", "ruff") {
			t.Fatalf("unexpected pipx path: %#v", pkg)
		}
	}
}

func TestPipxExecutablesMapsSuffixedCommands(t *testing.T) {
	root := pipxInventoryFixture(t)
	binaries, err := PipxExecutables()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ruff": "ruff", "ruff-format": "ruff", "ruff@dev": "ruff@dev"}
	if len(binaries) != len(want) {
		t.Fatalf("binaries = %#v", binaries)
	}
	assertPipxExecutablePaths(t, root, binaries, want)
}

func assertPipxExecutablePaths(t *testing.T, root string, binaries map[string]*core.PackageInfo, want map[string]string) {
	t.Helper()
	for command, identity := range want {
		pkg := binaries[command]
		validIdentity := pkg != nil && pkg.Name == identity
		if !validIdentity {
			t.Fatalf("%s mapped to %#v", command, pkg)
		}
		original := strings.TrimSuffix(command, "@dev")
		if pkg.Path != filepath.Join(root, "venvs", identity, "bin", original) {
			t.Fatalf("incorrect suffixed executable path: %#v", pkg)
		}
	}
}

func pipxInventoryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DIU_PIPX_JSON", strings.ReplaceAll(pipxFixtureJSON, "/pipx-home", root))
	prependFakeCommand(t, "pipx", "#!/bin/sh\nprintf '%s' \"$DIU_PIPX_JSON\"\n")
	return root
}

func TestGetInstalledPackagesPipxRejectsInvalidInventory(t *testing.T) {
	for _, output := range invalidPipxInventories() {
		t.Run(output, func(t *testing.T) {
			pipxInventoryFixture(t)
			t.Setenv("DIU_PIPX_JSON", output)
			if _, err := NewPipxMonitor().GetInstalledPackages(); err == nil {
				t.Fatal("invalid pipx inventory was accepted")
			}
		})
	}
}

func invalidPipxInventories() []string {
	return []string{
		`null`, `{}`, `{"pipx_spec_version":"9","venvs":{}}`,
		`{"pipx_spec_version":"0.1","venvs":{"ruff":{}}}`,
		strings.ReplaceAll(pipxFixtureJSON, `"package_version": "0.12.0"`, `"package_version": ""`),
		strings.ReplaceAll(pipxFixtureJSON, `"ruff": {`, `"--help": {`),
		strings.ReplaceAll(pipxFixtureJSON, `/venvs/ruff/bin/`, `/uv/tools/ruff/bin/`),
		strings.ReplaceAll(pipxFixtureJSON, `/venvs/ruff/bin/`, `/venvs/other/bin/`),
		strings.ReplaceAll(pipxFixtureJSON, `"__type__": "Path"`, `"__type__": "unknown"`),
		strings.ReplaceAll(pipxFixtureJSON, `"suffix": "@dev"`, `"suffix": "/unsafe"`),
	}
}

func TestGetInstalledPackagesPipxHandlesEmptyAndFailedInventory(t *testing.T) {
	pipxInventoryFixture(t)
	t.Setenv("DIU_PIPX_JSON", `{"pipx_spec_version":"0.1","venvs":{}}`)
	packages, err := NewPipxMonitor().GetInstalledPackages()
	isEmpty := err == nil && len(packages) == 0
	if !isEmpty {
		t.Fatalf("empty inventory = %v, %v", packages, err)
	}
	prependFakeCommand(t, "pipx", "#!/bin/sh\nprintf 'inventory failed' >&2\nexit 3\n")
	_, err = NewPipxMonitor().GetInstalledPackages()
	hasErrorDetail := err != nil && strings.Contains(err.Error(), "inventory failed")
	if !hasErrorDetail {
		t.Fatalf("pipx error detail = %v", err)
	}
}

func TestPipxExecutablesIncludesOnlyExposedDependencyCommands(t *testing.T) {
	root := pipxInventoryFixture(t)
	dependency := `"include_apps": true, "include_dependencies": true, "app_paths_of_dependencies": {"dep": [{"__type__":"Path","__Path__":"` + root + `/venvs/ruff/bin/dep-cli"}]}`
	output := strings.Replace(os.Getenv("DIU_PIPX_JSON"), `"include_apps": true`, dependency, 1)
	t.Setenv("DIU_PIPX_JSON", output)
	binaries, err := PipxExecutables()
	if err != nil {
		t.Fatal(err)
	}
	dependencyPackage := binaries["dep-cli"]
	isOwned := dependencyPackage != nil && dependencyPackage.Name == "ruff"
	if !isOwned {
		t.Fatalf("exposed dependency lost its owning install: %#v", binaries)
	}
	assertPipxDependencyUnexposed(t, output)
}

func assertPipxDependencyUnexposed(t *testing.T, output string) {
	t.Helper()
	t.Setenv("DIU_PIPX_JSON", strings.Replace(output, `"include_dependencies": true`, `"include_dependencies": false`, 1))
	binaries, err := PipxExecutables()
	isUnexposed := err == nil && binaries["dep-cli"] == nil
	if !isUnexposed {
		t.Fatalf("unexposed dependency attributed to pipx: %v, %v", binaries, err)
	}
}

func TestGetInstalledPackagesPipxLocatesDependencyOnlyInstall(t *testing.T) {
	root := pipxDependencyOnlyFixture(t)
	packages, err := NewPipxMonitor().GetInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "venvs", "ruff", "bin", "dep-cli")
	hasLocation := len(packages) == 2 && packages[0].Path == want
	if !hasLocation {
		t.Fatalf("dependency-only install lost its location: %#v", packages)
	}
}

func pipxDependencyOnlyFixture(t *testing.T) string {
	t.Helper()
	root := pipxInventoryFixture(t)
	var snapshot pipxSnapshot
	if err := json.Unmarshal([]byte(os.Getenv("DIU_PIPX_JSON")), &snapshot); err != nil {
		t.Fatal(err)
	}
	environment := snapshot.Venvs["ruff"]
	main := environment.Metadata.MainPackage
	main.Apps, main.AppPaths = nil, nil
	main.ShouldIncludeDependencies = true
	path := pipxPath{Type: "Path", Path: filepath.Join(root, "venvs", "ruff", "bin", "dep-cli")}
	main.DependencyPaths = map[string][]pipxPath{"dep": {path}}
	output, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIU_PIPX_JSON", string(output))
	return root
}

func TestGetInstalledPackagesPipxLocatesInjectedOnlyInstall(t *testing.T) {
	root := pipxInjectedOnlyFixture(t)
	packages, err := NewPipxMonitor().GetInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 {
		t.Fatalf("packages = %#v, want the parent install only", packages)
	}
	pkg := packages[0]
	hasIdentity := pkg.Name == "library@dev" && pkg.Version == "1.0.0"
	want := filepath.Join(root, "venvs", "library@dev", "bin", "plugin")
	isLocated := hasIdentity && pkg.Path == want
	if !isLocated {
		t.Fatalf("injected-only install lost identity or location: %#v", pkg)
	}
}

func pipxInjectedOnlyFixture(t *testing.T) string {
	t.Helper()
	root := pipxInventoryFixture(t)
	output := `{"pipx_spec_version":"0.1","venvs":{"library@dev":{"metadata":{
		"main_package":{"package":"library","package_version":"1.0.0","suffix":"@dev",
			"include_apps":true,"apps":[],"app_paths":[]},
		"injected_packages":{"plugin":{"package":"plugin","package_version":"2.0.0","suffix":"",
			"include_apps":true,"apps":["plugin"],"app_paths":[
				{"__type__":"Path","__Path__":"/pipx-home/venvs/library@dev/bin/plugin"}
			]}}
	}}}}`
	t.Setenv("DIU_PIPX_JSON", strings.ReplaceAll(output, "/pipx-home", root))
	return root
}

func TestGetInstalledPackagesPipxRejectsForeignInjectedLocation(t *testing.T) {
	pipxInjectedOnlyFixture(t)
	output := strings.ReplaceAll(os.Getenv("DIU_PIPX_JSON"), "/venvs/library@dev/bin/", "/venvs/other/bin/")
	t.Setenv("DIU_PIPX_JSON", output)
	if _, err := NewPipxMonitor().GetInstalledPackages(); err == nil {
		t.Fatal("injected command outside the parent install was accepted")
	}
}

func TestGetInstalledPackagesPipxSkipsUnexposedInjectedLocation(t *testing.T) {
	pipxInjectedOnlyFixture(t)
	output := strings.ReplaceAll(os.Getenv("DIU_PIPX_JSON"), `"include_apps":true`, `"include_apps":false`)
	t.Setenv("DIU_PIPX_JSON", output)
	packages, err := NewPipxMonitor().GetInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	isUnlocated := len(packages) == 1 && packages[0].Path == ""
	if !isUnlocated {
		t.Fatalf("unexposed injected command supplied a location: %#v", packages)
	}
}

func TestPipxExecutablesKeepsUnexposedInstallInInventory(t *testing.T) {
	pipxInventoryFixture(t)
	output := strings.Replace(os.Getenv("DIU_PIPX_JSON"), `"metadata": {`, `"metadata": {"exposure_enabled": false,`, 1)
	t.Setenv("DIU_PIPX_JSON", output)
	binaries, err := PipxExecutables()
	hasOneCommand := err == nil && len(binaries) == 1
	isVisibleInstall := hasOneCommand && binaries["ruff@dev"] != nil
	if !isVisibleInstall {
		t.Fatalf("unexposed install produced commands: %v, %v", binaries, err)
	}
	packages, err := NewPipxMonitor().GetInstalledPackages()
	hasBothInstalls := err == nil && len(packages) == 2
	if !hasBothInstalls {
		t.Fatalf("unexposed install lost from inventory: %v, %v", packages, err)
	}
}

const pipxFixtureJSON = `{
  "pipx_spec_version": "0.1",
  "venvs": {
    "ruff": {"metadata": {"main_package": {
      "package": "ruff", "package_version": "0.12.0", "suffix": "", "include_apps": true,
      "apps": ["ruff", "ruff-format"], "app_paths": [
        {"__type__": "Path", "__Path__": "/pipx-home/venvs/ruff/bin/ruff"},
        {"__type__": "Path", "__Path__": "/pipx-home/venvs/ruff/bin/ruff-format"}
      ]
    }}},
    "ruff@dev": {"metadata": {"main_package": {
      "package": "ruff", "package_version": "0.12.0", "suffix": "@dev", "include_apps": true,
      "apps": ["ruff"], "app_paths": [{"__type__": "Path", "__Path__": "/pipx-home/venvs/ruff@dev/bin/ruff"}]
    }}}
  }
}`
