package monitors

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestGetInstalledPackagesRubyGemsUsesSelectedCLIVersions(t *testing.T) {
	root := rubyGemsInventoryFixture(t)
	monitor := NewRubyGemsMonitor()
	if err := monitor.Initialize(core.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 3 {
		t.Fatalf("packages = %#v, want CLI gems including default gems", packages)
	}
	pkg := packages[0]
	hasIdentity := pkg.Name == "cli-tools" && pkg.Version == "2.0.0"
	isLocated := hasIdentity && pkg.Path == filepath.Join(root, "gems", "cli-tools-2.0.0")
	if !isLocated {
		t.Fatalf("unexpected selected gem: %#v", pkg)
	}
}

func TestRubyGemsInventoryTracksExposedSourceVersion(t *testing.T) {
	root := rubyGemsSourceFixture(t)
	output := readRealRubyGemsInventory(t, root)
	assertRubyGemsSourceVersion(t, output, root)
}

func TestRubyGemsInventoryIgnoresIncompleteNativeBuild(t *testing.T) {
	root := rubyGemsSourceFixture(t)
	path := filepath.Join(root, "specifications", "diu-version-fixture-2.0.0.gemspec")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	spec := strings.Replace(string(content), " s.summary", " s.extensions = ['ext/extconf.rb']\n s.summary", 1)
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "bin", "diu-gem-version")); err != nil {
		t.Fatal(err)
	}
	output := readRealRubyGemsInventory(t, root)
	assertRubyGemsSourceVersion(t, output, root)
}

func readRealRubyGemsInventory(t *testing.T, root string) []byte {
	t.Helper()
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("real Ruby is unavailable")
	}
	t.Setenv("GEM_HOME", root)
	t.Setenv("GEM_PATH", root)
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := exec.Command(ruby, "-rrubygems", "-rjson", "-e", rubyGemsInventoryScript).Output()
	if err != nil {
		t.Fatalf("RubyGems inventory: %v\n%s", err, output)
	}
	return output
}

func rubyGemsSourceFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "gem home")
	for _, version := range []string{"1.0.0", "2.0.0"} {
		rubyGemsSourceVersionFixture(t, root, version)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "gems", "diu-version-fixture-1.0.0", "bin", "diu-gem-version")
	if err := os.Symlink(source, filepath.Join(root, "bin", "diu-gem-version")); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func rubyGemsSourceVersionFixture(t *testing.T, root, version string) {
	t.Helper()
	bin := filepath.Join(root, "gems", "diu-version-fixture-"+version, "bin")
	for _, directory := range []string{bin, filepath.Join(root, "specifications")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	spec := fmt.Sprintf("Gem::Specification.new do |s|\n s.name = 'diu-version-fixture'\n s.version = '%s'\n s.summary = 'fixture'\n s.authors = ['DIU']\n s.executables = ['diu-gem-version']\nend\n", version)
	path := filepath.Join(root, "specifications", "diu-version-fixture-"+version+".gemspec")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFakeCommand(t, bin, "diu-gem-version", "#!/usr/bin/env ruby\nabort 'application must not run during inventory'\n")
}

func assertRubyGemsSourceVersion(t *testing.T, output []byte, root string) {
	t.Helper()
	snapshot, err := parseRubyGemsSnapshot(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, gem := range snapshot.Packages {
		if gem.Name != "diu-version-fixture" {
			continue
		}
		hasIdentity := gem.Version == "1.0.0" && len(gem.Executables) == 1
		isSelected := hasIdentity && gem.Path == filepath.Join(root, "gems", "diu-version-fixture-1.0.0")
		if !isSelected {
			t.Fatalf("inventory chose an unexposed version: %#v", gem)
		}
		return
	}
	t.Fatal("source-linked CLI gem is missing")
}

func rubyGemsInventoryFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "gem home")
	ruby := prependFakeCommand(t, "ruby", "#!/bin/sh\nprintf '%s' \"$DIU_GEM_JSON\"\n")
	t.Setenv("DIU_GEM_RUBY", ruby)
	prependFakeCommand(t, "gem", "#!/bin/sh\nprintf '  - RUBY EXECUTABLE: %s\\n' \"$DIU_GEM_RUBY\"\n")
	output := strings.ReplaceAll(rubyGemsFixtureJSON, "/gem-home", root)
	output = strings.ReplaceAll(output, "/active/ruby", ruby)
	t.Setenv("DIU_GEM_JSON", output)
	return root
}

func TestRubyGemsExecutablesRequiresOwnedWrappers(t *testing.T) {
	rubyGemsExecutableFixture(t)
	binaries, err := RubyGemsExecutables()
	if err != nil {
		t.Fatal(err)
	}
	if len(binaries) != 2 {
		t.Fatalf("binaries = %#v, want two verified commands", binaries)
	}
	for _, pkg := range binaries {
		if pkg.Name != "cli-tools" {
			t.Fatalf("unowned command attributed to a gem: %#v", pkg)
		}
	}
}

func rubyGemsExecutableFixture(t *testing.T) string {
	t.Helper()
	root := rubyGemsInventoryFixture(t)
	bin := t.TempDir()
	for _, name := range []string{"gem-one", "gem-two"} {
		script := fmt.Sprintf("#!%s\n# This file was generated by RubyGems.\nload Gem.activate_bin_path('cli-tools', '%s', version)\n", os.Getenv("DIU_GEM_RUBY"), name)
		writeFakeCommand(t, bin, name, script)
	}
	writeFakeCommand(t, bin, "gem-other", "#!/bin/sh\nexit 0\n")
	writeFakeCommand(t, bin, "unrelated", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root
}

func TestRubyGemsExecutablesRecognizesSymlinkedWrapper(t *testing.T) {
	rubyGemsExecutableFixture(t)
	bin := strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]
	path := filepath.Join(bin, "gem-one")
	target := filepath.Join(t.TempDir(), "wrapper")
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	binaries, err := RubyGemsExecutables()
	isOwned := err == nil && binaries["gem-one"] != nil
	if !isOwned {
		t.Fatalf("symlinked wrapper lost ownership: %v, %v", binaries, err)
	}
}

func TestRubyGemsExecutablesRecognizesDefaultGems(t *testing.T) {
	rubyGemsInventoryFixture(t)
	script := fmt.Sprintf("#!%s\n# This file was generated by RubyGems.\nload Gem.activate_bin_path('bundler', 'bundle', version)\n", os.Getenv("DIU_GEM_RUBY"))
	prependFakeCommand(t, "bundle", script)
	binaries, err := RubyGemsExecutables()
	isDefaultCLI := err == nil && binaries["bundle"] != nil
	if !isDefaultCLI {
		t.Fatalf("default CLI gem lost ownership: %v, %v", binaries, err)
	}
}

func TestRubyGemsWrapperMatchesGeneratedFormats(t *testing.T) {
	rubyGemsExecutableFixture(t)
	ruby := os.Getenv("DIU_GEM_RUBY")
	versioned := t.TempDir()
	if err := os.Symlink(ruby, filepath.Join(versioned, "ruby3.1")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", versioned+string(os.PathListSeparator)+os.Getenv("PATH"))
	flagged := fmt.Sprintf("#!%s -w", ruby)
	for _, shebang := range []string{flagged, "#!/usr/bin/env ruby3.1"} {
		t.Run(shebang, func(t *testing.T) { assertRubyGemsWrapperFormat(t, shebang, ruby) })
	}
}

func assertRubyGemsWrapperFormat(t *testing.T, shebang, ruby string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wrapper")
	script := shebang + "\n# This file was generated by RubyGems.\n  Gem.activate_and_load_bin_path('cli-tools', 'gem-one', version)\n"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	gem := rubyGemPackage{Name: "cli-tools"}
	if !rubyGemsWrapperMatches(path, gem, rubyGemExecutable{Name: "gem-one"}, ruby) {
		t.Fatalf("generated wrapper was rejected: %s", script)
	}
}

func TestGetInstalledPackagesRubyGemsRejectsInvalidInventory(t *testing.T) {
	for _, output := range []string{`null`, `{}`, `{"schema":1,"packages":null}`, strings.ReplaceAll(rubyGemsFixtureJSON, "/gem-home/gems/", "/other/gems/")} {
		t.Run(output, func(t *testing.T) {
			rubyGemsInventoryFixture(t)
			t.Setenv("DIU_GEM_JSON", output)
			if _, err := NewRubyGemsMonitor().GetInstalledPackages(); err == nil {
				t.Fatal("invalid RubyGems inventory was accepted")
			}
		})
	}
}

func TestGetInstalledPackagesRubyGemsHandlesEmptyAndFailedInventory(t *testing.T) {
	rubyGemsInventoryFixture(t)
	output := fmt.Sprintf(`{"schema":1,"ruby":%q,"packages":[]}`, os.Getenv("DIU_GEM_RUBY"))
	t.Setenv("DIU_GEM_JSON", output)
	packages, err := NewRubyGemsMonitor().GetInstalledPackages()
	isEmpty := err == nil && len(packages) == 0
	if !isEmpty {
		t.Fatalf("empty inventory = %v, %v", packages, err)
	}
	prependFakeCommand(t, "ruby", "#!/bin/sh\nprintf 'inventory failed' >&2\nexit 3\n")
	_, err = NewRubyGemsMonitor().GetInstalledPackages()
	hasError := err != nil && strings.Contains(err.Error(), "inventory failed")
	if !hasError {
		t.Fatalf("inventory failure = %v", err)
	}
}

func TestGetInstalledPackagesRubyGemsRejectsDifferentRubyRuntime(t *testing.T) {
	rubyGemsInventoryFixture(t)
	t.Setenv("DIU_GEM_RUBY", filepath.Join(t.TempDir(), "other-ruby"))
	if _, err := NewRubyGemsMonitor().GetInstalledPackages(); err == nil {
		t.Fatal("gem and ruby from different environments were accepted")
	}
}

const rubyGemsFixtureJSON = `{"schema":1,"ruby":"/active/ruby","packages":[
  {"name":"cli-tools","version":"2.0.0","root":"/gem-home","path":"/gem-home/gems/cli-tools-2.0.0","platform":"ruby","default":false,"executables":[
    {"name":"gem-one","path":"/gem-home/gems/cli-tools-2.0.0/bin/gem-one"},
    {"name":"gem-two","path":"/gem-home/gems/cli-tools-2.0.0/bin/gem-two"}
  ]},
  {"name":"other-cli","version":"1.0.0","root":"/gem-home","path":"/gem-home/gems/other-cli-1.0.0","platform":"ruby","default":false,"executables":[
    {"name":"gem-other","path":"/gem-home/gems/other-cli-1.0.0/bin/gem-other"}
  ]},
  {"name":"library","version":"1.0.0","root":"/gem-home","path":"/gem-home/gems/library-1.0.0","platform":"ruby","default":false,"executables":[]},
  {"name":"bundler","version":"2.5.0","root":"/gem-home","path":"/gem-home/gems/bundler-2.5.0","platform":"ruby","default":true,"executables":[
    {"name":"bundle","path":"/gem-home/gems/bundler-2.5.0/exe/bundle"}
  ]}
]}`
