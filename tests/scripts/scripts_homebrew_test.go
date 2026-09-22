package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type homebrewBinary struct {
	cpu      string
	arch     string
	checksum string
}

func TestHomebrewFormulaUsesReleaseBinaries(t *testing.T) {
	checksums := homebrewChecksums("0.2.3")
	baseURL := "https://github.com/yowainwright/diu/releases/download/v0.2.3"
	formula, err := renderHomebrewFormula(t, "v0.2.3", baseURL+"/", checksums)
	if err != nil {
		t.Fatalf("render formula: %v\n%s", err, formula)
	}
	arm := homebrewBinary{cpu: "arm", arch: "arm64", checksum: strings.Repeat("a", 64)}
	intel := homebrewBinary{cpu: "intel", arch: "amd64", checksum: strings.Repeat("b", 64)}
	assertHomebrewBinary(t, formula, baseURL, arm)
	assertHomebrewBinary(t, formula, baseURL, intel)
	for _, forbidden := range []string{`depends_on "go"`, `system "go"`, `head "`, "archive/refs/tags", "std_go_args"} {
		if strings.Contains(formula, forbidden) {
			t.Errorf("binary formula contains source-build instruction %q", forbidden)
		}
	}
	if !strings.Contains(formula, `bin.install "diu"`) {
		t.Fatal("formula does not install the archived binary")
	}
}

func assertHomebrewBinary(t *testing.T, formula, baseURL string, binary homebrewBinary) {
	t.Helper()
	block := "on_" + binary.cpu + " do\n" +
		`      url "` + baseURL + "/diu_0.2.3_darwin_" + binary.arch + ".tar.gz\"\n" +
		`      sha256 "` + binary.checksum + `"`
	if !strings.Contains(formula, block) {
		t.Errorf("formula is missing the %s archive/checksum pair:\n%s", binary.arch, formula)
	}
}

func TestHomebrewFormulaAcceptsLocalArchives(t *testing.T) {
	formula, err := renderHomebrewFormula(t, "0.2.3", "file:///workspace/dist", homebrewChecksums("0.2.3"))
	if err != nil {
		t.Fatalf("render local formula: %v\n%s", err, formula)
	}
	if !strings.Contains(formula, "file:///workspace/dist/diu_0.2.3_darwin_arm64.tar.gz") {
		t.Fatal("local archive URL was not preserved")
	}
}

func TestHomebrewFormulaRejectsInvalidReleaseInputs(t *testing.T) {
	for name, input := range invalidHomebrewInputs() {
		t.Run(name, func(t *testing.T) {
			output, err := renderHomebrewFormula(t, input[0], input[1], input[2])
			if err == nil {
				t.Fatalf("invalid input produced a formula:\n%s", output)
			}
			if strings.Contains(output, "class Diu") {
				t.Fatalf("invalid input produced a partial formula:\n%s", output)
			}
		})
	}
}

func invalidHomebrewInputs() map[string][3]string {
	checksums := homebrewChecksums("0.2.3")
	baseURL := "https://example.com/releases/v0.2.3"
	missingIntel := strings.SplitN(checksums, "\n", 2)[0] + "\n"
	interpolatedURL := baseURL + "#{abort}"
	quotedURL := baseURL + `"`
	duplicateChecksums := checksums + checksums
	invalidChecksums := strings.ReplaceAll(checksums, "aaaa", "xxxx")
	return map[string][3]string{
		"version":            {"broken", baseURL, checksums},
		"insecure URL":       {"0.2.3", "http://example.com", checksums},
		"Ruby interpolation": {"0.2.3", interpolatedURL, checksums},
		"URL quotes":         {"0.2.3", quotedURL, checksums},
		"missing checksum":   {"0.2.3", baseURL, missingIntel},
		"duplicate checksum": {"0.2.3", baseURL, duplicateChecksums},
		"invalid checksum":   {"0.2.3", baseURL, invalidChecksums},
		"wrong version":      {"0.2.4", baseURL, checksums},
	}
}

func homebrewChecksums(version string) string {
	arm64 := strings.Repeat("a", 64) + "  diu_" + version + "_darwin_arm64.tar.gz\n"
	amd64 := strings.Repeat("b", 64) + "  diu_" + version + "_darwin_amd64.tar.gz\n"
	return arm64 + amd64
}

func renderHomebrewFormula(t *testing.T, version, baseURL, checksums string) (string, error) {
	t.Helper()
	checksumsPath := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte(checksums), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(projectRoot(t), "ops", "scripts", "render-homebrew-formula.sh")
	cmd := exec.Command("bash", script, version, baseURL, checksumsPath)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
