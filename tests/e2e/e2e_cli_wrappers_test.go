//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIWrappersPreserveCommandContract(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			f := newCLIFixture(t)
			f.setup(t)
			for _, command := range []string{"probe", "brew"} {
				assertCLICommandContract(t, f, shell, command)
			}
		})
	}
}

func assertCLICommandContract(t *testing.T, f *cliFixture, shell, name string) {
	t.Helper()
	assertCLISelectedCommand(t, f, shell, filepath.Join(f.wrappers, name))
	assertCLICommandBehavior(t, f, shell, name)
}

func assertCLIUnwrappedCommandContract(t *testing.T, f *cliFixture) {
	t.Helper()
	assertCLISelectedCommand(t, f, "bash", filepath.Join(f.managed, "probe"))
	assertCLICommandBehavior(t, f, "bash", "probe")
}

func assertCLISelectedCommand(t *testing.T, f *cliFixture, shell, expected string) {
	t.Helper()
	args := []string{"/bin/bash", "--noprofile", "--norc", "-c", `command -v "$1"`, "diu-test", filepath.Base(expected)}
	result := runCLICommand(t, f.shell(t, shell, args...), "")
	assertCLIExit(t, result, 0)
	if strings.TrimSpace(result.stdout) != expected {
		t.Fatalf("%s selected %q, want %s", shell, result.stdout, expected)
	}
}

func assertCLICommandBehavior(t *testing.T, f *cliFixture, shell, name string) {
	t.Helper()
	args := cliProbeArgs()
	original := filepath.Join(f.managed, name)
	if name == "brew" {
		original = filepath.Join(f.bin, name)
	}
	input := "stdin must survive\nwithout a trailing newline"
	want := runCLICommand(t, f.command(t, original, args...), input)
	got := runCLICommand(t, f.shell(t, shell, append([]string{name}, args...)...), input)
	assertCLIExit(t, want, 17)
	if got != want {
		t.Fatalf("%s changed command behavior:\ngot: %#v\nwant: %#v", shell, got, want)
	}
}

func cliProbeArgs() []string {
	return []string{"argument", "", "with spaces", "quotes'\"$`", "line\nbreak", "日本語"}
}

func (f *cliFixture) shell(t *testing.T, shell string, args ...string) *exec.Cmd {
	t.Helper()
	switch shell {
	case "bash":
		prefix := []string{"--noprofile", "--norc", "-c", `. "$HOME/.bashrc"; exec "$@"`, "diu-test"}
		return f.command(t, "/bin/bash", append(prefix, args...)...)
	case "zsh":
		prefix := []string{"-f", "-c", `source "$HOME/.zshrc"; exec "$@"`, "diu-test"}
		return f.command(t, "/usr/bin/zsh", append(prefix, args...)...)
	default:
		prefix := []string{"--no-config", "-c", `source "$HOME/.config/fish/config.fish"; exec $argv`, "--"}
		return f.command(t, "/usr/bin/fish", append(prefix, args...)...)
	}
}

func TestCLIWrapperDirectoryWithShellCharacters(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			f := newCLIFixture(t)
			f.wrappers = filepath.Join(f.home, "wrappers $'`\" spaces")
			process := &f.config.Monitoring.Process
			process.WrapperDir = f.wrappers
			writeCLIConfig(t, f)
			f.setup(t)
			assertCLICommandContract(t, f, shell, "probe")
			assertCLIExit(t, f.cli(t, "uninstall"), 0)
			assertCLICleanShells(t, f)
		})
	}
}

func TestCLINestedPATHSelectionAndPersonalScripts(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	preferred := filepath.Join(f.home, "preferred")
	writeCLIFile(t, filepath.Join(preferred, "probe"), nestedProbeScript, 0o700)
	writeCLIFile(t, filepath.Join(preferred, "personal-helper"), "#!/bin/sh\nprintf 'wrong\\n'\n", 0o700)
	writeCLIFile(t, filepath.Join(f.wrappers, "personal-helper"), "#!/bin/sh\nprintf 'personal\\n'\n", 0o700)
	f.env = append(f.env, "PATH="+preferred+":"+f.basePath())
	for range 2 {
		result := runCLICommand(t, f.shell(t, "bash", "probe"), "")
		assertCLIExit(t, result, 23)
		if result.stdout != "preferred\npersonal\n" {
			t.Fatalf("nested PATH selection changed: %#v", result)
		}
	}
	assertCLIDelegates(t, f)
}

const nestedProbeScript = `#!/bin/bash
if [ "${1:-}" = inner ]; then printf 'preferred\n'; exit 23; fi
probe inner
personal-helper
exit 23
`

func assertCLIDelegates(t *testing.T, f *cliFixture) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(f.wrappers, ".diu-delegates", "*", "probe"))
	hasWrongCount := err != nil || len(paths) != 1
	if hasWrongCount {
		t.Fatalf("expected one reusable delegation file: %v, %v", paths, err)
	}
	info, err := os.Stat(paths[0])
	hasWrongMode := err != nil || info.Mode().Perm() != 0o700
	if hasWrongMode {
		t.Fatalf("delegate permissions: %v, %v", info, err)
	}
	if !strings.Contains(readCLIFile(t, paths[0]), "# DIU delegated command") {
		t.Fatal("unrecognized delegation file")
	}
}

func TestCLIReturningShimsDoNotLoop(t *testing.T) {
	for _, invocation := range []string{`exec "$DIU_TEST_WRAPPER" "$@"`, `"$DIU_TEST_WRAPPER" "$@"; exit $?`} {
		t.Run(invocation, func(t *testing.T) {
			f := newCLIFixture(t)
			f.setup(t)
			shimDir := filepath.Join(f.home, "shims")
			guard := "#!/bin/bash\n[ -z \"${DIU_SHIM_RETURNED:-}\" ] || exit 99\nexport DIU_SHIM_RETURNED=1\n"
			writeCLIFile(t, filepath.Join(shimDir, "probe"), guard+invocation+"\n", 0o700)
			f.env = append(f.env, "PATH="+shimDir+":"+f.basePath(), "DIU_TEST_WRAPPER="+f.wrappers+"/probe")
			assertCLICommandContract(t, f, "bash", "probe")
		})
	}
}
