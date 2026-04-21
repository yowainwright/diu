package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const hookMarker = "# diu shell hooks"

var integrationBinary string

type listedPackage struct {
	Name       string `json:"name"`
	Tool       string `json:"tool"`
	Version    string `json:"version"`
	UsageCount int    `json:"usage_count"`
}

func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	repoRoot := filepath.Dir(wd)
	buildDir, err := os.MkdirTemp("", "diu-integration-build-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(buildDir)

	integrationBinary = filepath.Join(buildDir, "diu")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", integrationBinary, "./cmd/diu")
	cmd.Dir = repoRoot
	cmd.Env = mergeEnv(os.Environ(), map[string]string{
		"GOCACHE": filepath.Join(buildDir, "gocache"),
	})
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, string(output))
		os.Exit(1)
	}

	os.Exit(m.Run())
}

type cliEnv struct {
	t       *testing.T
	home    string
	fakeBin string
	goBin   string
}

func newCLIEnv(t *testing.T) *cliEnv {
	t.Helper()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	fakeBin := filepath.Join(root, "bin")
	goBin := filepath.Join(home, "go", "bin")

	for _, dir := range []string{home, fakeBin, goBin} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create %s: %v", dir, err)
		}
	}

	for _, rcFile := range []string{".zshrc", ".bashrc"} {
		path := filepath.Join(home, rcFile)
		if err := os.WriteFile(path, []byte("# test shell rc\n"), 0644); err != nil {
			t.Fatalf("failed to seed %s: %v", path, err)
		}
	}

	return &cliEnv{t: t, home: home, fakeBin: fakeBin, goBin: goBin}
}

func (e *cliEnv) env() []string {
	return mergeEnv(os.Environ(), map[string]string{
		"HOME":   e.home,
		"PATH":   e.fakeBin + ":" + os.Getenv("PATH"),
		"GOBIN":  e.goBin,
		"GOPATH": filepath.Join(e.home, "go"),
	})
}

func (e *cliEnv) run(args ...string) (string, int) {
	e.t.Helper()

	cmd := exec.Command(integrationBinary, args...)
	cmd.Env = e.env()
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		e.t.Fatalf("failed to run diu %v: %v", args, err)
	}
	return string(output), exitErr.ExitCode()
}

func (e *cliEnv) writeScript(name, body string) {
	e.t.Helper()

	if !strings.HasPrefix(body, "#!") {
		body = "#!/bin/sh\n" + body
	}

	path := filepath.Join(e.fakeBin, name)
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		e.t.Fatalf("failed to write %s: %v", path, err)
	}
}

func (e *cliEnv) writeGoBinary(name, version string) {
	e.t.Helper()

	body := fmt.Sprintf(`#!/bin/sh
case "$1" in
  version|--version)
    printf '%%s\n' '%s'
    ;;
  *)
    exit 0
    ;;
esac
`, version)

	path := filepath.Join(e.goBin, name)
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		e.t.Fatalf("failed to write fake go binary %s: %v", path, err)
	}
}

func (e *cliEnv) read(path string) string {
	e.t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(data)
}

func (e *cliEnv) listPackages(tool string) []listedPackage {
	e.t.Helper()

	args := []string{"list", "--format", "json"}
	if tool != "" {
		args = append(args, "--tool", tool)
	}

	output, code := e.run(args...)
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

func TestCLISetupAndTeardown(t *testing.T) {
	env := newCLIEnv(t)

	if output, code := env.run("setup"); code != 0 {
		t.Fatalf("diu setup exited %d: %s", code, output)
	}
	if output, code := env.run("setup"); code != 0 {
		t.Fatalf("diu setup (repeat) exited %d: %s", code, output)
	}

	for _, rcFile := range []string{".zshrc", ".bashrc"} {
		path := filepath.Join(env.home, rcFile)
		content := env.read(path)
		if count := strings.Count(content, hookMarker); count != 1 {
			t.Fatalf("expected one hook block in %s, got %d\n%s", path, count, content)
		}
	}

	statusOutput, code := env.run("status")
	if code != 0 {
		t.Fatalf("diu status exited %d: %s", code, statusOutput)
	}
	if !strings.Contains(statusOutput, filepath.Join(env.home, ".zshrc")) {
		t.Fatalf("status output missing .zshrc path: %s", statusOutput)
	}

	if output, code := env.run("teardown"); code != 0 {
		t.Fatalf("diu teardown exited %d: %s", code, output)
	}

	for _, rcFile := range []string{".zshrc", ".bashrc"} {
		path := filepath.Join(env.home, rcFile)
		content := env.read(path)
		if strings.Contains(content, hookMarker) {
			t.Fatalf("expected hooks removed from %s\n%s", path, content)
		}
	}
}

func TestCLIScanUsesStableToolAliases(t *testing.T) {
	env := newCLIEnv(t)

	env.writeScript("brew", `case "$1 $2 $3" in
"info --installed --json=v2")
  cat <<'JSON'
{"formulae":[{"name":"wget","dependencies":["libidn2"],"linked_keg":"1.2.3","installed":[{"version":"1.2.3","time":1711929600}]}]}
JSON
  ;;
"list --cask ")
  printf '%s\n' 'firefox'
  ;;
*)
  exit 0
  ;;
esac
`)

	env.writeScript("go", `exit 0`)
	env.writeGoBinary("stringer", "stringer version v1.2.3")

	if output, code := env.run("scan", "--tool", "brew"); code != 0 {
		t.Fatalf("diu scan --tool brew exited %d: %s", code, output)
	}
	brewPackages := env.listPackages("brew")
	if len(brewPackages) != 2 {
		t.Fatalf("expected 2 brew packages, got %d: %#v", len(brewPackages), brewPackages)
	}
	if brewPackages[0].Tool != "homebrew" || brewPackages[1].Tool != "homebrew" {
		t.Fatalf("expected homebrew-normalized tools, got %#v", brewPackages)
	}

	if output, code := env.run("scan", "--tool", "go"); code != 0 {
		t.Fatalf("diu scan --tool go exited %d: %s", code, output)
	}
	goPackages := env.listPackages("go")
	if len(goPackages) != 1 {
		t.Fatalf("expected 1 go package, got %d: %#v", len(goPackages), goPackages)
	}
	if goPackages[0].Name != "stringer" || goPackages[0].Version != "v1.2.3" {
		t.Fatalf("unexpected go package payload: %#v", goPackages[0])
	}
}

func TestCLIRecordTracksSingleExecution(t *testing.T) {
	env := newCLIEnv(t)

	if output, code := env.run("record", "--tool", "brew", "--exit-code", "0", "--", "install", "wget"); code != 0 {
		t.Fatalf("diu record exited %d: %s", code, output)
	}
	if output, code := env.run("record", "--tool", "brew", "--exit-code", "1", "--", "install", "curl"); code != 0 {
		t.Fatalf("diu record failure-path exited %d: %s", code, output)
	}

	packages := env.listPackages("brew")
	if len(packages) != 1 {
		t.Fatalf("expected one recorded package, got %d: %#v", len(packages), packages)
	}
	if packages[0].Name != "wget" || packages[0].UsageCount != 1 {
		t.Fatalf("unexpected package payload: %#v", packages[0])
	}

	statsOutput, code := env.run("stats")
	if code != 0 {
		t.Fatalf("diu stats exited %d: %s", code, statsOutput)
	}
	if !strings.Contains(statsOutput, "Total executions recorded: 1") {
		t.Fatalf("expected stats output to report one execution: %s", statsOutput)
	}
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
