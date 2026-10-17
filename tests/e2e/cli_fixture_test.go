//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
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
