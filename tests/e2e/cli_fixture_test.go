//go:build e2e

package e2e

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

const cliBinary = "/usr/local/bin/diu"

type cliFixture struct {
	home    string
	bin     string
	managed string
	config  *core.Config
	env     []string
}

func newCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	home := shortTestHome(t)
	f := &cliFixture{home: home, bin: filepath.Join(home, "bin"), managed: filepath.Join(home, "managed")}
	f.env = []string{"HOME=" + home, "PATH=" + f.basePath(), "LANG=C.UTF-8", "DIU_TEST_HOME=" + home}
	f.env = append(f.env, "DIU_TEST_TOKEN=preserve me", "TMPDIR="+home)
	writeCLIManagers(t, f)
	writeCLIShells(t, f)
	f.config = cliConfig(f)
	writeCLIConfig(t, f)
	return f
}

func shortTestHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "diu-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	return home
}

func (f *cliFixture) basePath() string {
	return strings.Join([]string{f.bin, f.managed, "/usr/bin", "/bin"}, ":")
}

func cliConfig(f *cliFixture) *core.Config {
	config := core.DefaultConfig()
	data := filepath.Join(f.home, ".local", "share", "diu")
	config.Daemon = core.DaemonConfig{DataDir: data, PIDFile: data + "/diu.pid", SocketPath: data + "/diu.sock"}
	config.Storage.JSONFile = filepath.Join(data, "executions.json")
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{core.ToolHomebrew: {f.managed}}
	config.Monitoring.Filesystem.ScanInterval = time.Hour
	config.Tools.Homebrew.CellarPaths = []string{filepath.Join(f.home, "Cellar")}
	config.Tools.Homebrew.ShouldTrackCasks = false
	config.API.IsEnabled = false
	return config
}

func writeCLIConfig(t *testing.T, f *cliFixture) {
	t.Helper()
	data, err := json.Marshal(f.config)
	if err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, filepath.Join(f.home, ".config", "diu", "config.json"), string(data), 0o600)
}

func writeCLIFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func writeCLIManagers(t *testing.T, f *cliFixture) {
	t.Helper()
	writeCLIFile(t, filepath.Join(f.bin, "brew"), brewFixtureScript, 0o700)
	writeCLIFile(t, filepath.Join(f.managed, "probe"), probeFixtureScript, 0o700)
	if err := os.Symlink(cliBinary, filepath.Join(f.bin, "diu")); err != nil {
		t.Fatal(err)
	}
}

func newCLIKrewFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLIFixture(t)
	root := filepath.Join(f.home, "krew root")
	f.env = append(f.env, "KREW_ROOT="+root, "PATH="+filepath.Join(root, "bin")+":"+f.basePath())
	monitoring := &f.config.Monitoring
	monitoring.EnabledTools = []string{core.ToolKrew}
	monitoring.Filesystem.WatchPaths = map[string][]string{}
	writeCLIConfig(t, f)
	for _, name := range []string{"ctx", "view-secret", "krew", "get"} {
		writeCLIKrewPlugin(t, root, name)
	}
	writeCLIFile(t, filepath.Join(f.bin, "kubectl"), krewFixtureScript, 0o700)
	writeCLIFile(t, filepath.Join(root, "store", "krew", "v1.2.3", "plugin"), krewUninstallFixtureScript, 0o700)
	return f
}

func writeCLIKrewPlugin(t *testing.T, root, name string) {
	t.Helper()
	writeCLIFile(t, filepath.Join(root, "receipts", name+".yaml"), "kind: Plugin\n", 0o600)
	path := filepath.Join(root, "store", name, "v1.2.3", "plugin")
	writeCLIFile(t, path, "#!/bin/sh\nexit 0\n", 0o700)
	link := filepath.Join(root, "bin", "kubectl-"+strings.ReplaceAll(name, "-", "_"))
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
}

const krewFixtureScript = `#!/bin/sh
if [ "$1" = help ]; then
    if [ "$2" = get ]; then printf 'Usage:\n  kubectl get [flags] [options]\n'; exit 0; fi
    printf 'Unknown help topic [\140%s\140]\n\nUsage:\n  kubectl [flags] [options]\n' "$2"
    exit 0
fi
case "$1" in get|version|-*) exit 0 ;; esac
plugin=$(printf '%s' "$1" | tr '-' '_')
shift
exec "$KREW_ROOT/bin/kubectl-$plugin" "$@"
`

const krewUninstallFixtureScript = `#!/bin/sh
[ "$1" = uninstall ] || exit 1
printf '%s\n' "$@" > "$DIU_TEST_HOME/krew-uninstall-args"
[ ! -f "$DIU_TEST_HOME/krew-uninstall-failed" ] || exit 7
name="$2"
plugin=$(printf '%s' "$name" | tr '-' '_')
rm -f "$KREW_ROOT/bin/kubectl-$plugin" "$KREW_ROOT/receipts/$name.yaml"
rm -rf "$KREW_ROOT/store/$name"
`

func newCLIRealKrewFixture(t *testing.T) *cliFixture {
	t.Helper()
	directory := os.Getenv("DIU_KREW_VERIFICATION_DIR")
	if directory == "" {
		t.Skip("real Krew verification binaries are not mounted")
	}
	f := newCLIFixture(t)
	root := filepath.Join(f.home, "krew root")
	f.env = append(f.env, "KREW_ROOT="+root, "KREW_NO_UPGRADE_CHECK=1", "PATH="+filepath.Join(root, "bin")+":"+f.basePath())
	monitoring := &f.config.Monitoring
	monitoring.EnabledTools = []string{core.ToolKrew}
	monitoring.Filesystem.WatchPaths = map[string][]string{}
	writeCLIConfig(t, f)
	if err := os.Symlink(filepath.Join(directory, "kubectl"), filepath.Join(f.bin, "kubectl")); err != nil {
		t.Fatal(err)
	}
	installCLIRealKrew(t, f, directory, root)
	return f
}

func installCLIRealKrew(t *testing.T, f *cliFixture, directory, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "index", "default", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	platform := "krew-linux_" + runtime.GOARCH
	binary := filepath.Join(directory, "krew", platform)
	manifest := "--manifest=" + filepath.Join(directory, "krew.yaml")
	archive := "--archive=" + filepath.Join(directory, platform+".tar.gz")
	result := runCLICommand(t, f.command(t, binary, "install", manifest, archive, "--no-update-index"), "")
	assertCLIExit(t, result, 0)
	pluginArchive := writeCLIRealKrewArchive(t, f)
	for _, name := range []string{"ctx", "view-secret", "get", "Custom_tool"} {
		installCLIRealKrewPlugin(t, f, name, pluginArchive)
	}
}

func writeCLIRealKrewArchive(t *testing.T, f *cliFixture) string {
	t.Helper()
	var contents bytes.Buffer
	writer := zip.NewWriter(&contents)
	header := &zip.FileHeader{Name: "plugin"}
	header.SetMode(0o700)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("#!/bin/sh\nexit 0\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.home, "plugin.zip")
	writeCLIFile(t, path, contents.String(), 0o600)
	return path
}

func installCLIRealKrewPlugin(t *testing.T, f *cliFixture, name, archive string) {
	t.Helper()
	digest := sha256.Sum256([]byte(readCLIFile(t, archive)))
	manifest := fmt.Sprintf("apiVersion: krew.googlecontainertools.github.com/v1alpha2\nkind: Plugin\nmetadata:\n  name: %s\nspec:\n  version: v1.2.3\n  homepage: https://example.invalid\n  shortDescription: Offline DIU fixture\n  platforms:\n  - selector:\n      matchLabels:\n        os: linux\n        arch: %s\n    uri: https://example.invalid/plugin.zip\n    sha256: %x\n    bin: plugin\n", name, runtime.GOARCH, digest)
	path := filepath.Join(f.home, name+".yaml")
	writeCLIFile(t, path, manifest, 0o600)
	result := runCLICommand(t, f.command(t, filepath.Join(f.bin, "kubectl"), "krew", "install", "--manifest="+path, "--archive="+archive, "--no-update-index"), "")
	assertCLIExit(t, result, 0)
}

func newCLICargoFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLIFixture(t)
	root := filepath.Join(f.home, "cargo-root")
	f.env = append(f.env, "CARGO_INSTALL_ROOT="+root, "CARGO_HOME="+filepath.Join(f.home, ".cargo"))
	f.env = append(f.env, "PATH="+filepath.Join(root, "bin")+":"+f.basePath())
	monitoring := &f.config.Monitoring
	monitoring.EnabledTools = []string{core.ToolCargo}
	monitoring.Filesystem.WatchPaths = map[string][]string{}
	writeCLIConfig(t, f)
	writeCLIFile(t, filepath.Join(f.bin, "cargo"), cargoFixtureScript, 0o700)
	writeCLIFile(t, filepath.Join(f.home, "cargo-inventory"), cargoFixtureInventory, 0o600)
	for _, name := range []string{"rg", "tool-one", "tool-two", "unrelated"} {
		path := filepath.Join(root, "bin", name)
		writeCLIFile(t, path, "#!/bin/sh\nprintf '%s\\n' \"${0##*/}\"\n", 0o700)
	}
	return f
}

func newCLIPipxFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLIFixture(t)
	root, bin := filepath.Join(f.home, "pipx root"), filepath.Join(f.home, "shared-bin")
	f.env = append(f.env, "PIPX_HOME="+root, "PIPX_BIN_DIR="+bin, "PATH="+bin+":"+f.basePath())
	monitoring := &f.config.Monitoring
	monitoring.EnabledTools = []string{core.ToolPipx}
	monitoring.Filesystem.WatchPaths = map[string][]string{}
	writeCLIConfig(t, f)
	writeCLIFile(t, filepath.Join(f.bin, "pipx"), pipxFixtureScript, 0o700)
	full := `{"pipx_spec_version":"0.1","venvs":{` + pipxBaseEntry + "," + pipxSuffixedEntry + "}}"
	remaining := `{"pipx_spec_version":"0.1","venvs":{` + pipxBaseEntry + "}}"
	writeCLIFile(t, filepath.Join(f.home, "pipx-inventory"), strings.ReplaceAll(full, "/pipx-home", root), 0o600)
	writeCLIFile(t, filepath.Join(f.home, "pipx-remaining"), strings.ReplaceAll(remaining, "/pipx-home", root), 0o600)
	writeCLIPipxExecutables(t, f, root, bin)
	return f
}

func newCLIGemFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLIFixture(t)
	root := filepath.Join(f.home, "gem home")
	f.env = append(f.env, "GEM_HOME="+root, "GEM_PATH="+root, "PATH="+root+"/bin:"+f.basePath())
	monitoring := &f.config.Monitoring
	monitoring.EnabledTools = []string{core.ToolGem}
	monitoring.Filesystem.WatchPaths = map[string][]string{}
	writeCLIConfig(t, f)
	writeCLIFile(t, filepath.Join(f.bin, "ruby"), gemRubyFixtureScript, 0o700)
	writeCLIFile(t, filepath.Join(f.bin, "gem"), gemManagerFixtureScript, 0o700)
	writeCLIGemInventory(t, f, root)
	writeCLIGemCommands(t, f, root)
	return f
}

func writeCLIGemInventory(t *testing.T, f *cliFixture, root string) {
	t.Helper()
	prefix := `{"schema":1,"ruby":"` + filepath.Join(f.bin, "ruby") + `","packages":[`
	full := prefix + gemToolsEntry + "," + gemOtherEntry + "]}"
	older := strings.ReplaceAll(gemToolsEntry, "2.0.0", "1.0.0")
	remaining := prefix + older + "," + gemOtherEntry + "]}"
	writeCLIFile(t, filepath.Join(f.home, "gem-inventory"), strings.ReplaceAll(full, "/gem-home", root), 0o600)
	writeCLIFile(t, filepath.Join(f.home, "gem-remaining"), strings.ReplaceAll(remaining, "/gem-home", root), 0o600)
}

func writeCLIGemCommands(t *testing.T, f *cliFixture, root string) {
	t.Helper()
	for _, command := range []string{"gem-one", "gem-two"} {
		writeCLIGemCommand(t, f, root, "cli-tools-2.0.0", command)
		old := filepath.Join(root, "gems", "cli-tools-1.0.0", "bin", command)
		writeCLIFile(t, old, "older gem version\n", 0o700)
	}
	writeCLIGemCommand(t, f, root, "other-cli-1.0.0", "gem-other")
	writeCLIFile(t, filepath.Join(root, "bin", "unrelated"), "#!/bin/sh\nexit 0\n", 0o700)
}

func writeCLIGemCommand(t *testing.T, f *cliFixture, root, fullName, command string) {
	t.Helper()
	name := strings.TrimSuffix(fullName, "-2.0.0")
	name = strings.TrimSuffix(name, "-1.0.0")
	source := filepath.Join(root, "gems", fullName, "bin", command)
	writeCLIFile(t, source, "#!/bin/sh\nexit 0\n", 0o700)
	shebang := "#!" + filepath.Join(f.bin, "ruby") + "\n"
	call := "load Gem.activate_bin_path('" + name + "', '" + command + "', version)\n"
	wrapper := shebang + "# This file was generated by RubyGems.\n" + call
	writeCLIFile(t, filepath.Join(root, "bin", command), wrapper, 0o700)
}

const gemToolsEntry = `{"name":"cli-tools","version":"2.0.0","root":"/gem-home","path":"/gem-home/gems/cli-tools-2.0.0","platform":"ruby","default":false,"executables":[
  {"name":"gem-one","path":"/gem-home/gems/cli-tools-2.0.0/bin/gem-one"},
  {"name":"gem-two","path":"/gem-home/gems/cli-tools-2.0.0/bin/gem-two"}
]}`

const gemOtherEntry = `{"name":"other-cli","version":"1.0.0","root":"/gem-home","path":"/gem-home/gems/other-cli-1.0.0","platform":"ruby","default":false,"executables":[
  {"name":"gem-other","path":"/gem-home/gems/other-cli-1.0.0/bin/gem-other"}
]}`

const gemRubyFixtureScript = `#!/bin/sh
set -eu
if [ "$1" != -rrubygems ]; then exit 0; fi
if [ -f "$HOME/gem-scan-failed" ]; then
    printf 'gem inventory unavailable\n' >&2
    exit 1
fi
/bin/cat "$HOME/gem-inventory"
`

const gemManagerFixtureScript = `#!/bin/sh
set -eu
case "$1" in
    environment) printf '  - RUBY EXECUTABLE: %s/bin/ruby\n' "$HOME" ;;
    --version) printf '3.3.15\n' ;;
    uninstall)
        printf '%s\n' "$@" > "$HOME/gem-uninstall-args"
        if [ -f "$HOME/gem-dependent" ]; then printf 'dependent gem exists\n' >&2; exit 1; fi
        if [ -f "$HOME/gem-uninstall-failed" ]; then exit 7; fi
        [ "$2" = cli-tools ] && [ "$3" = --version ] && [ "$4" = 2.0.0 ]
        [ "$5" = --install-dir ] && [ "$6" = "$GEM_HOME" ]
        /bin/rm "$GEM_HOME/gems/cli-tools-2.0.0/bin/gem-one" "$GEM_HOME/gems/cli-tools-2.0.0/bin/gem-two"
        /bin/cp "$HOME/gem-remaining" "$HOME/gem-inventory"
        ;;
    *) exit 2 ;;
esac
`

func writeCLIPipxExecutables(t *testing.T, f *cliFixture, root, bin string) {
	t.Helper()
	writeCLIFile(t, filepath.Join(bin, "uv-owned"), "#!/bin/sh\nexit 0\n", 0o700)
	for _, suffix := range []string{"", "@dev"} {
		writeCLIPipxCommand(t, f, root, suffix, "px-one")
		writeCLIPipxCommand(t, f, root, suffix, "px-two")
	}
}

func writeCLIPipxCommand(t *testing.T, f *cliFixture, root, suffix, command string) {
	t.Helper()
	path := filepath.Join(root, "venvs", "px-tools"+suffix, "bin", command)
	writeCLIFile(t, path, "#!/bin/sh\nexit 0\n", 0o700)
	exposed := filepath.Join(f.home, "shared-bin", command+suffix)
	if err := os.Symlink(path, exposed); err != nil {
		t.Fatal(err)
	}
}

const pipxBaseEntry = `"px-tools":{"metadata":{"main_package":{
  "package":"px-tools","package_version":"1.0.0","suffix":"","include_apps":true,
  "apps":["px-one","px-two"],"app_paths":[
    {"__type__":"Path","__Path__":"/pipx-home/venvs/px-tools/bin/px-one"},
    {"__type__":"Path","__Path__":"/pipx-home/venvs/px-tools/bin/px-two"}
  ]
}}}`

const pipxSuffixedEntry = `"px-tools@dev":{"metadata":{"main_package":{
  "package":"px-tools","package_version":"1.0.0","suffix":"@dev","include_apps":true,
  "apps":["px-one","px-two"],"app_paths":[
    {"__type__":"Path","__Path__":"/pipx-home/venvs/px-tools@dev/bin/px-one"},
    {"__type__":"Path","__Path__":"/pipx-home/venvs/px-tools@dev/bin/px-two"}
  ]
}}}`

const pipxFixtureScript = `#!/bin/sh
set -eu
case "$1" in
    list)
        [ "$2" = --json ]
        if [ -f "$HOME/pipx-scan-failed" ]; then
            printf 'pipx inventory unavailable\n' >&2
            exit 1
        fi
        /bin/cat "$HOME/pipx-inventory"
        ;;
    uninstall)
        printf '%s\n' "$@" > "$HOME/pipx-uninstall-args"
        if [ -f "$HOME/pipx-uninstall-failed" ]; then exit 7; fi
        [ "$#" -eq 2 ] && [ "$2" = px-tools@dev ]
        /bin/rm "$PIPX_BIN_DIR/px-one@dev" "$PIPX_BIN_DIR/px-two@dev"
        /bin/rm "$PIPX_HOME/venvs/px-tools@dev/bin/px-one" "$PIPX_HOME/venvs/px-tools@dev/bin/px-two"
        /bin/cp "$HOME/pipx-remaining" "$HOME/pipx-inventory"
        ;;
    --version) printf 'pipx fixture\n' ;;
    *) exit 2 ;;
esac
`

const cargoFixtureInventory = `cli-tools v1.2.3 (/fixture/source):
    tool-one
    tool-two
ripgrep v14.1.1:
    rg
`

const cargoFixtureScript = `#!/bin/sh
set -eu
case "$1" in
    install)
        [ "$2" = --list ]
        if [ -f "$HOME/cargo-scan-failed" ]; then
            printf 'cargo inventory unavailable\n' >&2
            exit 1
        fi
        /bin/cat "$HOME/cargo-inventory"
        ;;
    uninstall)
        printf '%s\n' "$@" > "$HOME/cargo-uninstall-args"
        if [ -f "$HOME/cargo-uninstall-failed" ]; then exit 7; fi
        [ "$2" = --root ] && [ "$3" = "$CARGO_INSTALL_ROOT" ] && [ "$4" = ripgrep@14.1.1 ]
        /bin/rm "$CARGO_INSTALL_ROOT/bin/rg"
        /bin/sed '/^ripgrep /,$d' "$HOME/cargo-inventory" > "$HOME/cargo-next"
        /bin/mv "$HOME/cargo-next" "$HOME/cargo-inventory"
        ;;
    --version) printf 'cargo fixture\n' ;;
    *) exit 2 ;;
esac
`

const probeFixtureScript = `#!/bin/bash
printf 'cwd=<%s>\ntoken=<%s>\n' "$PWD" "$DIU_TEST_TOKEN"
printf 'argument=<%s>\n' "$@"
/bin/cat
printf 'original stderr\n' >&2
exit 17
`

const brewFixtureScript = `#!/bin/bash
case "${1:-}" in
    --cellar) printf '%s/Cellar\n' "$HOME" ;;
    --prefix) printf '%s\n' "$HOME" ;;
    info) printf '{"formulae":[],"casks":[]}\n' ;;
    list) exit 0 ;;
    *) exec "$HOME/managed/probe" "$@" ;;
esac
`

var shellFiles = []string{".bashrc", ".zshrc", ".config/fish/config.fish"}

const shellSentinel = "# personal settings: retain this line\n"

func writeCLIShells(t *testing.T, f *cliFixture) {
	t.Helper()
	for _, name := range shellFiles {
		writeCLIFile(t, filepath.Join(f.home, name), shellSentinel, 0o640)
	}
	writeCLIFile(t, filepath.Join(f.home, ".zprofile"), shellSentinel, 0o640)
	writeCLIFile(t, filepath.Join(f.home, "personal.txt"), "must survive\n", 0o600)
}
