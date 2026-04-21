//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const hookMarker = "# diu shell hooks"

type listedPackage struct {
	Name       string `json:"name"`
	Tool       string `json:"tool"`
	UsageCount int    `json:"usage_count"`
}

type dockerEnv struct {
	t       *testing.T
	home    string
	fakeBin string
}

func TestShellHooksRecordCommandsFromFreshZsh(t *testing.T) {
	if os.Getenv("DIU_E2E_DOCKER") != "1" {
		t.Skip("docker e2e only")
	}

	env := newDockerEnv(t)
	env.writeScript("brew", `exit 0`)

	if output, code := env.runDIU("setup"); code != 0 {
		t.Fatalf("diu setup exited %d: %s", code, output)
	}

	if output, code := env.runShell("zsh", "brew install wget"); code != 0 {
		t.Fatalf("zsh hook flow exited %d: %s", code, output)
	}

	pkg := env.waitForPackage("brew", "wget", 5*time.Second)
	if pkg.UsageCount != 1 {
		t.Fatalf("expected wget usage count 1, got %#v", pkg)
	}
}

func TestShellHooksCanBeRemovedCleanly(t *testing.T) {
	if os.Getenv("DIU_E2E_DOCKER") != "1" {
		t.Skip("docker e2e only")
	}

	env := newDockerEnv(t)
	env.writeScript("brew", `case "$1 $2 $3" in
"info --installed --json=v2")
  cat <<'JSON'
{"formulae":[{"name":"wget","dependencies":[],"linked_keg":"1.2.3","installed":[{"version":"1.2.3","time":1711929600}]}]}
JSON
  ;;
*)
  exit 0
  ;;
esac
`)

	if output, code := env.runDIU("setup"); code != 0 {
		t.Fatalf("diu setup exited %d: %s", code, output)
	}
	if output, code := env.runDIU("scan", "--tool", "brew"); code != 0 {
		t.Fatalf("diu scan exited %d: %s", code, output)
	}
	if output, code := env.runDIU("teardown"); code != 0 {
		t.Fatalf("diu teardown exited %d: %s", code, output)
	}

	for _, rcFile := range []string{".zshrc", ".bashrc"} {
		path := filepath.Join(env.home, rcFile)
		content := env.read(path)
		if strings.Contains(content, hookMarker) {
			t.Fatalf("expected hooks removed from %s\n%s", path, content)
		}
	}

	if output, code := env.runShell("bash", "brew install curl"); code != 0 {
		t.Fatalf("bash post-teardown flow exited %d: %s", code, output)
	}
	time.Sleep(500 * time.Millisecond)

	packages := env.listPackages("brew")
	if len(packages) != 1 || packages[0].Name != "wget" {
		t.Fatalf("expected only scanned package to remain, got %#v", packages)
	}
}

func newDockerEnv(t *testing.T) *dockerEnv {
	t.Helper()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	fakeBin := filepath.Join(root, "bin")

	for _, dir := range []string{home, fakeBin} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create %s: %v", dir, err)
		}
	}

	for _, rcFile := range []string{".zshrc", ".bashrc"} {
		path := filepath.Join(home, rcFile)
		if err := os.WriteFile(path, []byte("# docker e2e rc\n"), 0644); err != nil {
			t.Fatalf("failed to seed %s: %v", path, err)
		}
	}

	return &dockerEnv{t: t, home: home, fakeBin: fakeBin}
}

func (e *dockerEnv) env() []string {
	return mergeEnv(os.Environ(), map[string]string{
		"HOME": e.home,
		"PATH": e.fakeBin + ":" + os.Getenv("PATH"),
	})
}

func (e *dockerEnv) runDIU(args ...string) (string, int) {
	e.t.Helper()
	return e.run("/usr/local/bin/diu", args...)
}

func (e *dockerEnv) runShell(shell, command string) (string, int) {
	e.t.Helper()

	args := []string{"-ic", command}
	return e.run(shell, args...)
}

func (e *dockerEnv) run(name string, args ...string) (string, int) {
	e.t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Env = e.env()
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		e.t.Fatalf("failed to run %s %v: %v", name, args, err)
	}
	return string(output), exitErr.ExitCode()
}

func (e *dockerEnv) writeScript(name, body string) {
	e.t.Helper()

	if !strings.HasPrefix(body, "#!") {
		body = "#!/bin/sh\n" + body
	}

	path := filepath.Join(e.fakeBin, name)
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		e.t.Fatalf("failed to write %s: %v", path, err)
	}
}

func (e *dockerEnv) read(path string) string {
	e.t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(data)
}

func (e *dockerEnv) listPackages(tool string) []listedPackage {
	e.t.Helper()

	args := []string{"list", "--format", "json"}
	if tool != "" {
		args = append(args, "--tool", tool)
	}

	output, code := e.runDIU(args...)
	if code != 0 {
		e.t.Fatalf("diu %v exited %d: %s", args, code, output)
	}

	if !strings.HasPrefix(strings.TrimSpace(output), "[") {
		return nil
	}

	var packages []listedPackage
	if err := json.Unmarshal([]byte(output), &packages); err != nil {
		e.t.Fatalf("failed to decode package list %q: %v", output, err)
	}

	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Tool != packages[j].Tool {
			return packages[i].Tool < packages[j].Tool
		}
		return packages[i].Name < packages[j].Name
	})

	return packages
}

func (e *dockerEnv) waitForPackage(tool, name string, timeout time.Duration) listedPackage {
	e.t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, pkg := range e.listPackages(tool) {
			if pkg.Name == name {
				return pkg
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	e.t.Fatalf("timed out waiting for %s package %s", tool, name)
	return listedPackage{}
}

func mergeEnv(base []string, overrides map[string]string) []string {
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	filtered := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, skip := overrides[key]; skip {
				continue
			}
		}
		filtered = append(filtered, entry)
	}

	for _, key := range keys {
		filtered = append(filtered, key+"="+overrides[key])
	}

	return filtered
}
