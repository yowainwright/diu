package wrappers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestNewWrapperGenerator(t *testing.T) {
	config := core.DefaultConfig()
	gen := NewWrapperGenerator(config)

	if gen == nil {
		t.Fatal("Expected non-nil generator")
	}

	if gen.config != config {
		t.Error("Config not set correctly")
	}
}

func TestGenerateWrapper(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.API.Host = "127.0.0.1"
	config.API.Port = 8080

	gen := NewWrapperGenerator(config)

	err := gen.GenerateWrapper("test-tool", "/usr/bin/test-tool")
	if err != nil {
		t.Fatalf("GenerateWrapper failed: %v", err)
	}

	wrapperPath := filepath.Join(tmpDir, "test-tool")
	if _, err := os.Stat(wrapperPath); os.IsNotExist(err) {
		t.Error("Wrapper file not created")
	}

	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("Failed to read wrapper: %v", err)
	}

	script := string(content)

	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Wrapper should start with shebang")
	}

	if !strings.Contains(script, "/usr/bin/test-tool") {
		t.Error("Wrapper should contain original path")
	}

	if !strings.Contains(script, "test-tool") {
		t.Error("Wrapper should contain tool name")
	}

	if !strings.Contains(script, "http://127.0.0.1:8080/api/v1/executions") {
		t.Error("Wrapper should contain API endpoint")
	}

	info, _ := os.Stat(wrapperPath)
	if info.Mode()&0111 == 0 {
		t.Error("Wrapper should be executable")
	}
}

func TestGenerateWrapperCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	wrapperDir := filepath.Join(tmpDir, "nested", "wrapper", "dir")

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperDir
	config.API.Host = "localhost"
	config.API.Port = 8080

	gen := NewWrapperGenerator(config)

	err := gen.GenerateWrapper("test", "/usr/bin/test")
	if err != nil {
		t.Fatalf("GenerateWrapper failed: %v", err)
	}

	if _, err := os.Stat(wrapperDir); os.IsNotExist(err) {
		t.Error("Wrapper directory not created")
	}
}

func TestFindOriginalBinaryWithMapping(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = "/nonexistent"
	gen := NewWrapperGenerator(config)

	path, err := gen.findOriginalBinary("ls")
	if err != nil {
		t.Skip("ls not found in PATH")
	}

	if !strings.Contains(path, "ls") {
		t.Errorf("Expected path to contain 'ls', got %s", path)
	}
}

func TestFindOriginalBinaryHomebrewMapping(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = "/nonexistent"
	gen := NewWrapperGenerator(config)

	_, err := gen.findOriginalBinary("homebrew")
	if err != nil {
		if !strings.Contains(err.Error(), "brew") {
			t.Errorf("Expected error to mention 'brew', got %v", err)
		}
	}
}

func TestFindOriginalBinaryPythonMapping(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = "/nonexistent"
	gen := NewWrapperGenerator(config)

	_, err := gen.findOriginalBinary("python")
	if err != nil {
		if !strings.Contains(err.Error(), "pip") {
			t.Errorf("Expected error to mention 'pip', got %v", err)
		}
	}
}

func TestFindOriginalBinarySkipsWrapperDir(t *testing.T) {
	tmpDir := t.TempDir()

	wrapperBinary := filepath.Join(tmpDir, "mytool")
	os.WriteFile(wrapperBinary, []byte("#!/bin/bash"), 0755)

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	gen := NewWrapperGenerator(config)

	_, err := gen.findOriginalBinary("mytool")
	if err == nil {
		t.Error("Should not find wrapper directory binary")
	}
}

func TestFindOriginalBinaryNotFound(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = "/nonexistent"
	gen := NewWrapperGenerator(config)

	_, err := gen.findOriginalBinary("nonexistent-binary-xyz123")
	if err == nil {
		t.Error("Expected error for nonexistent binary")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 'not found' in error, got %v", err)
	}
}

func TestInstallWrappers(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.EnabledTools = []string{"ls"}
	config.API.Host = "localhost"
	config.API.Port = 8080

	gen := NewWrapperGenerator(config)

	err := gen.InstallWrappers()
	if err != nil {
		t.Skip("ls not found in PATH")
	}

	wrapperPath := filepath.Join(tmpDir, "ls")
	if _, err := os.Stat(wrapperPath); os.IsNotExist(err) {
		t.Error("Wrapper not created for ls")
	}
}

func TestInstallWrappersSkipsNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.EnabledTools = []string{"nonexistent-tool-xyz123"}
	config.API.Host = "localhost"
	config.API.Port = 8080

	gen := NewWrapperGenerator(config)

	err := gen.InstallWrappers()
	if err != nil {
		t.Errorf("InstallWrappers should not fail for missing tools: %v", err)
	}
}

func TestUpdatePATH(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := t.TempDir()

	bashrc := filepath.Join(homeDir, ".bashrc")
	os.WriteFile(bashrc, []byte("# existing content\n"), 0644)

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir

	gen := NewWrapperGenerator(config)
	gen.updatePATH()
}

func TestContainsHelper(t *testing.T) {
	tests := []struct {
		s      string
		substr string
		want   bool
	}{
		{"hello world", "world", true},
		{"hello world", "hello", false},
		{"test", "test", true},
		{"abc", "abcd", false},
		{"", "", true},
		{"abc", "", true},
	}

	for _, tt := range tests {
		got := contains(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestWrapperTemplateContent(t *testing.T) {
	if !strings.Contains(wrapperTemplate, "#!/bin/bash") {
		t.Error("Template should have bash shebang")
	}

	if !strings.Contains(wrapperTemplate, "{{.Tool}}") {
		t.Error("Template should have Tool placeholder")
	}

	if !strings.Contains(wrapperTemplate, "{{.OriginalPath}}") {
		t.Error("Template should have OriginalPath placeholder")
	}

	if !strings.Contains(wrapperTemplate, "{{.APIEndpoint}}") {
		t.Error("Template should have APIEndpoint placeholder")
	}

	if !strings.Contains(wrapperTemplate, "curl") {
		t.Error("Template should use curl for API calls")
	}

	if !strings.Contains(wrapperTemplate, "exit $EXIT_CODE") {
		t.Error("Template should preserve exit code")
	}
}

func TestSimpleWrapperTemplateContent(t *testing.T) {
	if !strings.Contains(simpleWrapperTemplate, "#!/bin/bash") {
		t.Error("Simple template should have bash shebang")
	}

	if !strings.Contains(simpleWrapperTemplate, "{{.Tool}}") {
		t.Error("Simple template should have Tool placeholder")
	}

	if !strings.Contains(simpleWrapperTemplate, "{{.OriginalPath}}") {
		t.Error("Simple template should have OriginalPath placeholder")
	}

	if !strings.Contains(simpleWrapperTemplate, "diu.sock") {
		t.Error("Simple template should use socket")
	}
}
