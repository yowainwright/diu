package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

func assertInvalidReportingFlags(t *testing.T, args []string) {
	t.Helper()
	var err error
	output := captureStdout(t, func() {
		err = newRootCommand().Execute(args)
	})
	if err == nil {
		t.Fatal("invalid flags succeeded")
	}
	if output != "" {
		t.Fatalf("invalid flags printed a report: %q", output)
	}
}

func TestRootCommandStyleguide(t *testing.T) {
	t.Setenv("DIU_COLOR", "always")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root := newRootCommand()
	root.Output = &stdout
	root.ErrorOutput = &stderr
	if err := root.Execute([]string{"--styleguide"}); err != nil {
		t.Fatalf("styleguide failed: %v", err)
	}
	assertStyleguideOutput(t, stdout.String())
	assertEmptyHelpStderr(t, stderr.String())
}

func assertStyleguideOutput(t *testing.T, output string) {
	t.Helper()

	for _, text := range []string{"DIU Styleguide", "Tones", "Status", "Table", "Progress", "\x1b["} {
		if !strings.Contains(output, text) {
			t.Fatalf("styleguide = %q, want %q", output, text)
		}
	}
}

func assertOutputContains(t *testing.T, output, value string) {
	t.Helper()

	if !strings.Contains(output, value) {
		t.Fatalf("Expected %q in output, got: %q", value, output)
	}
}

func assertOutputOmits(t *testing.T, output, value string) {
	t.Helper()

	if strings.Contains(output, value) {
		t.Fatalf("Expected %q to be omitted, got: %q", value, output)
	}
}

func TestVersionString(t *testing.T) {
	vs := versionString()
	if vs == "" {
		t.Fatal("versionString returned empty")
	}
	if !strings.Contains(vs, "diu") {
		t.Fatalf("Expected 'diu' in version string, got: %q", vs)
	}
}

func TestCloseStore(t *testing.T) {
	config := setupTestHomeConfig(t)
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	closeStore(store)
}

func TestIsTerminal(t *testing.T) {
	result := isTerminal()
	_ = result
}

func TestCoreVersion(t *testing.T) {
	version := coreVersion()
	if version == "" {
		t.Fatal("coreVersion returned empty")
	}
	if !isAllowedCoreVersion(version) {
		t.Fatalf("coreVersion returned unexpected value: %q", version)
	}
}

func isAllowedCoreVersion(version string) bool {
	if version == core.Version {
		return true
	}
	return version == "dev"
}

func TestVersionStringConsistency(t *testing.T) {
	vs := versionString()
	if vs == "" {
		t.Fatal("versionString returned empty")
	}
	if !strings.HasPrefix(vs, "diu ") {
		t.Fatalf("versionString should start with 'diu ', got: %q", vs)
	}
	if !strings.Contains(vs, "commit ") {
		t.Fatalf("versionString should contain 'commit ', got: %q", vs)
	}
	if !strings.Contains(vs, "built ") {
		t.Fatalf("versionString should contain 'built ', got: %q", vs)
	}
	if !strings.Contains(vs, coreVersion()) {
		t.Fatalf("versionString should contain coreVersion, got: %q", vs)
	}
}

func jsonCommandOutput(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("DIU_COLOR", "always")
	return captureStdout(t, func() {
		if err := newRootCommand().Execute(args); err != nil {
			t.Fatal(err)
		}
	})
}

func assertJSONField(t *testing.T, report map[string]json.RawMessage, key, want string) {
	t.Helper()
	if string(report[key]) != want {
		t.Fatalf("%s = %s, want %s", key, report[key], want)
	}
}

func assertInvalidReportFormats(t *testing.T, name string) {
	t.Helper()
	for _, format := range []string{"", "yaml", "csv"} {
		assertInvalidReportingFlags(t, []string{name, "--format", format})
	}
}

func TestDurationHelpers(t *testing.T) {
	assertParsedDuration(t, "2d", 48*time.Hour)
	assertParsedDuration(t, "1w", 7*24*time.Hour)
	assertParsedDuration(t, "1mo", 30*24*time.Hour)
	assertParsedDuration(t, "30m", 30*time.Minute)
	assertParsedDuration(t, "3h", 3*time.Hour)

}

func assertParsedDuration(t *testing.T, value string, want time.Duration) {
	t.Helper()

	got, err := parseDuration(value)
	matches := err == nil && got == want
	if !matches {
		t.Fatalf("parseDuration %s = %s, %v; want %s", value, got, err, want)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	capture := newOutputCapture(t)
	oldStdout := os.Stdout
	os.Stdout = capture.writer
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()
	capture.closeWriter(t)
	return capture.output(t, "stdout")
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	capture := newOutputCapture(t)
	oldStderr := os.Stderr
	os.Stderr = capture.writer
	defer func() {
		os.Stderr = oldStderr
	}()

	fn()

	capture.closeWriter(t)
	return capture.output(t, "stderr")
}

type outputCapture struct {
	reader *os.File
	writer *os.File
}

func newOutputCapture(t *testing.T) outputCapture {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	return outputCapture{reader: reader, writer: writer}
}

func (capture outputCapture) closeWriter(t *testing.T) {
	t.Helper()

	if err := capture.writer.Close(); err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}
}

func (capture outputCapture) output(t *testing.T, label string) string {
	t.Helper()

	data, err := io.ReadAll(capture.reader)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", label, err)
	}
	if err := capture.reader.Close(); err != nil {
		t.Fatalf("Failed to close reader: %v", err)
	}
	return string(data)
}

func withReadOnlyStdout(t *testing.T, fn func()) {
	t.Helper()

	stdoutPath := filepath.Join(t.TempDir(), "stdout")
	if err := os.WriteFile(stdoutPath, nil, core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create read-only stdout target: %v", err)
	}
	file, err := os.Open(stdoutPath)
	if err != nil {
		t.Fatalf("Failed to open read-only stdout target: %v", err)
	}

	oldStdout := os.Stdout
	os.Stdout = file
	defer func() {
		os.Stdout = oldStdout
		if err := file.Close(); err != nil {
			t.Fatalf("Failed to close read-only stdout target: %v", err)
		}
	}()

	fn()
}

func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()

	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create stdin pipe: %v", err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatalf("Failed to write stdin: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close stdin writer: %v", err)
	}

	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		if err := reader.Close(); err != nil {
			t.Fatalf("Failed to close stdin reader: %v", err)
		}
	}()

	fn()
}

func parseTestFlags(t *testing.T, cmd *command, args ...string) {
	t.Helper()
	remaining, err := cmd.Flags().Parse(args)
	if err != nil {
		t.Fatalf("Failed to parse test flags %v: %v", args, err)
	}
	if len(remaining) != 0 {
		t.Fatalf("Unexpected remaining test args: %v", remaining)
	}
}
