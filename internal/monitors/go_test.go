package monitors

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestGoMonitor(t *testing.T) {
	monitor := NewGoMonitor()

	if monitor.Name() != core.ToolGo {
		t.Errorf("Expected monitor name '%s', got %s", core.ToolGo, monitor.Name())
	}
}

func TestGoMonitorStart(t *testing.T) {
	if err := NewGoMonitor().(*GoMonitor).Start(context.Background(), make(chan *core.ExecutionRecord)); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
}

func TestGoMonitorInitialize(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewGoMonitor().(*GoMonitor)
	err := monitor.Initialize(config)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if monitor.goPath == "" {
		t.Error("Expected goPath to be set")
	}

	if monitor.goBin == "" {
		t.Error("Expected goBin to be set")
	}
}

func TestGoMonitorInitializeWithConfig(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Go.GoPath = "/custom/gopath"
	config.Tools.Go.GoBin = "/custom/gobin"

	monitor := NewGoMonitor().(*GoMonitor)
	err := monitor.Initialize(config)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if monitor.goPath != "/custom/gopath" {
		t.Errorf("Expected goPath '/custom/gopath', got %s", monitor.goPath)
	}

	if monitor.goBin != "/custom/gobin" {
		t.Errorf("Expected goBin '/custom/gobin', got %s", monitor.goBin)
	}
}

type goParseCommandCase struct {
	name     string
	args     []string
	packages []string
	metadata map[string]interface{}
}

var goParseCommandCases = []goParseCommandCase{
	{
		name:     "get package",
		args:     []string{"get", "github.com/example/cobra"},
		packages: []string{"github.com/example/cobra"},
		metadata: map[string]interface{}{
			"subcommand": "get",
			"action":     "get",
		},
	},
	{
		name:     "get with update flag",
		args:     []string{"get", "-u", "github.com/gin-gonic/gin"},
		packages: []string{"github.com/gin-gonic/gin"},
		metadata: map[string]interface{}{
			"subcommand": "get",
			"action":     "get",
			"update":     true,
		},
	},
	{
		name:     "install package",
		args:     []string{"install", "github.com/golangci/golangci-lint/cmd/golangci-lint@latest"},
		packages: []string{"github.com/golangci/golangci-lint/cmd/golangci-lint@latest"},
		metadata: map[string]interface{}{
			"subcommand": "install",
			"action":     "install",
		},
	},
	{
		name:     "mod download",
		args:     []string{"mod", "download"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand":  "mod",
			"mod_command": "download",
			"action":      "mod_download",
		},
	},
	{
		name:     "mod tidy",
		args:     []string{"mod", "tidy"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand":  "mod",
			"mod_command": "tidy",
			"action":      "mod_tidy",
		},
	},
	{
		name:     "mod vendor",
		args:     []string{"mod", "vendor"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand":  "mod",
			"mod_command": "vendor",
			"action":      "mod_vendor",
		},
	},
	{
		name:     "mod init",
		args:     []string{"mod", "init", "github.com/user/project"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand":  "mod",
			"mod_command": "init",
			"module":      "github.com/user/project",
		},
	},
	{
		name:     "build",
		args:     []string{"build", "./..."},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "build",
			"action":     "build",
		},
	},
	{
		name:     "build with output",
		args:     []string{"build", "-o", "myapp", "./cmd/app"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "build",
			"action":     "build",
			"output":     "myapp",
		},
	},
	{
		name:     "build with -o= syntax",
		args:     []string{"build", "-o=myapp"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "build",
			"action":     "build",
			"output":     "myapp",
		},
	},
	{
		name:     "run file",
		args:     []string{"run", "main.go"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "run",
			"action":     "run",
			"file":       "main.go",
		},
	},
	{
		name:     "test all",
		args:     []string{"test", "./..."},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "test",
			"action":     "test",
		},
	},
	{
		name:     "test specific package",
		args:     []string{"test", "github.com/user/project/pkg"},
		packages: []string{"github.com/user/project/pkg"},
		metadata: map[string]interface{}{
			"subcommand": "test",
			"action":     "test",
		},
	},
	{
		name:     "fmt",
		args:     []string{"fmt", "./..."},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "fmt",
			"action":     "fmt",
		},
	},
	{
		name:     "vet",
		args:     []string{"vet", "./..."},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "vet",
			"action":     "vet",
		},
	},
	{
		name:     "list modules",
		args:     []string{"list", "-m", "all"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "list",
			"action":     "list",
			"modules":    true,
		},
	},
	{
		name:     "clean",
		args:     []string{"clean"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "clean",
			"action":     "clean",
		},
	},
	{
		name:     "clean modcache",
		args:     []string{"clean", "-modcache"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "clean",
			"action":     "clean",
			"modcache":   true,
		},
	},
	{
		name:     "env",
		args:     []string{"env"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "env",
			"action":     "env",
		},
	},
	{
		name:     "version",
		args:     []string{"version"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "version",
			"action":     "version",
		},
	},
}

func TestGoParseCommand(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	for _, tt := range goParseCommandCases {
		t.Run(tt.name, func(t *testing.T) {
			assertGoParseCommand(t, monitor, tt)
		})
	}
}

func assertGoParseCommand(t *testing.T, monitor *GoMonitor, test goParseCommandCase) {
	t.Helper()

	record, err := monitor.ParseCommand("go", test.args)
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	assertGoPackages(t, record.PackagesAffected, test.packages)
	assertGoMetadata(t, record.Metadata, test.metadata)
}

func assertGoPackages(t *testing.T, got []string, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("Expected %d packages, got %d: %v", len(want), len(got), got)
	}
	for i, pkg := range want {
		if i >= len(got) {
			continue
		}
		if got[i] != pkg {
			t.Errorf("Expected package %s, got %s", pkg, got[i])
		}
	}
}

func assertGoMetadata(t *testing.T, got map[string]interface{}, want map[string]interface{}) {
	t.Helper()

	for key, expectedVal := range want {
		val, exists := got[key]
		metadataMatches := exists && val == expectedVal
		if !metadataMatches {
			t.Errorf("Expected metadata %s=%v, got %v", key, expectedVal, val)
		}
	}
}

func TestGoParseCommandEmptyArgs(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	record, err := monitor.ParseCommand("go", []string{})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}

	if record.Tool != core.ToolGo {
		t.Errorf("Expected tool '%s', got %s", core.ToolGo, record.Tool)
	}

	if len(record.PackagesAffected) != 0 {
		t.Errorf("Expected no packages, got %v", record.PackagesAffected)
	}
}

func TestGoExtractGoPackages(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	for _, tt := range goExtractPackageCases {
		t.Run(tt.name, func(t *testing.T) {
			assertGoExtractPackages(t, monitor, tt)
		})
	}
}

type goExtractPackageCase struct {
	name     string
	args     []string
	expected []string
}

var goExtractPackageCases = []goExtractPackageCase{
	{
		name:     "single package",
		args:     []string{"github.com/example/cobra"},
		expected: []string{"github.com/example/cobra"},
	},
	{
		name:     "multiple packages",
		args:     []string{"github.com/example/cobra", "github.com/example/viper"},
		expected: []string{"github.com/example/cobra", "github.com/example/viper"},
	},
	{
		name:     "package with version",
		args:     []string{"github.com/example/cobra@v1.8.0"},
		expected: []string{"github.com/example/cobra@v1.8.0"},
	},
	{name: "skip flags", args: []string{"-u", "github.com/example/cobra", "-v"}, expected: []string{"github.com/example/cobra"}},
	{name: "skip current directory patterns", args: []string{".", "./...", "..."}, expected: nil},
	{name: "simple package name", args: []string{"mypackage"}, expected: []string{"mypackage"}},
	{name: "empty args", args: []string{}, expected: nil},
}

func assertGoExtractPackages(t *testing.T, monitor *GoMonitor, test goExtractPackageCase) {
	t.Helper()

	packages := monitor.extractGoPackages(test.args)
	if len(packages) != len(test.expected) {
		t.Errorf("Expected %d packages, got %d: %v", len(test.expected), len(packages), packages)
		return
	}
	for i, pkg := range test.expected {
		if packages[i] != pkg {
			t.Errorf("Expected package %s at index %d, got %s", pkg, i, packages[i])
		}
	}
}

func TestGoExtractOutputFlag(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	for _, tt := range goOutputFlagCases {
		t.Run(tt.name, func(t *testing.T) {
			output := monitor.extractOutputFlag(tt.args)
			if output != tt.expected {
				t.Errorf("Expected output '%s', got '%s'", tt.expected, output)
			}
		})
	}
}

type goOutputFlagCase struct {
	name     string
	args     []string
	expected string
}

var goOutputFlagCases = []goOutputFlagCase{
	{name: "no output flag", args: []string{"build", "./..."}, expected: ""},
	{name: "-o flag", args: []string{"build", "-o", "myapp", "./cmd"}, expected: "myapp"},
	{name: "-o= syntax", args: []string{"build", "-o=myapp"}, expected: "myapp"},
	{name: "-o at end", args: []string{"-o", "output"}, expected: "output"},
	{name: "-o without value", args: []string{"build", "-o"}, expected: ""},
}

func TestGoGetBinaries(t *testing.T) {
	tmpDir := t.TempDir()
	monitor := initializedGoMonitor(t, tmpDir)
	writeGoBinaryFixtures(t, tmpDir)

	packages, err := monitor.binaries()
	if err != nil {
		t.Fatalf("binaries failed: %v", err)
	}
	assertGoBinaryPackage(t, packages)
}

func assertGoBinaryPackage(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	if len(packages) != 1 {
		t.Errorf("Expected 1 binary, got %d", len(packages))
	}
	if len(packages) == 0 {
		return
	}
	assertGoBinaryFields(t, packages[0])
}

func assertGoBinaryFields(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	if pkg.Name != "testbin" {
		t.Errorf("Expected binary name 'testbin', got %s", pkg.Name)
	}
	if pkg.Tool != core.ToolGoBinary {
		t.Errorf("Expected tool '%s', got %s", core.ToolGoBinary, pkg.Tool)
	}
	if pkg.Fingerprint != "" {
		t.Error("Expected monitor to defer binary fingerprinting to inventory merge")
	}
	hasSignature := pkg.SizeBytes != 0 && pkg.ModifiedAt != 0
	if !hasSignature {
		t.Error("Expected binary size and modification signature")
	}
}

func initializedGoMonitor(t *testing.T, goBin string) *GoMonitor {
	t.Helper()

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Go.GoBin = goBin
	monitor := NewGoMonitor().(*GoMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func writeGoBinaryFixtures(t *testing.T, tmpDir string) {
	t.Helper()

	executablePath := filepath.Join(tmpDir, "testbin")
	executableContent := []byte("#!/bin/bash\necho 'testbin version v1.0.0'")
	writeMonitorExecutable(t, executablePath, executableContent)
	writeGoNonBinaryFixtures(t, tmpDir)
}

func writeMonitorExecutable(t *testing.T, path string, content []byte) {
	t.Helper()

	if err := os.WriteFile(path, content, core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create test executable: %v", err)
	}
	if err := os.Chmod(path, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark test executable: %v", err)
	}
}

func writeGoNonBinaryFixtures(t *testing.T, tmpDir string) {
	t.Helper()

	nonExecPath := filepath.Join(tmpDir, "nonexec")
	if err := os.WriteFile(nonExecPath, []byte("not executable"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create non-executable: %v", err)
	}
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}
}

func TestGoGetBinaryVersionDoesNotExecuteBinary(t *testing.T) {
	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, "tool")
	markerPath := filepath.Join(tempDir, "executed")
	script := "#!/bin/sh\ntouch " + markerPath + "\n"
	if err := os.WriteFile(binaryPath, []byte(script), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write binary: %v", err)
	}
	if err := os.Chmod(binaryPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod binary: %v", err)
	}

	monitor := NewGoMonitor().(*GoMonitor)
	if _, err := monitor.binaryVersion(binaryPath); err == nil {
		t.Fatal("binaryVersion should reject a non-Go binary")
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("binary was executed, marker stat error = %v", err)
	}
}

func TestGoGetBinariesNonExistentDir(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Go.GoBin = "/nonexistent/path/that/does/not/exist"

	monitor := NewGoMonitor().(*GoMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	packages, err := monitor.binaries()
	if err != nil {
		t.Fatalf("binaries should not error for non-existent dir: %v", err)
	}

	if len(packages) != 0 {
		t.Errorf("Expected nil or empty packages, got %v", packages)
	}
}

func TestGoGetBinariesEmptyGoBin(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)
	monitor.goBin = ""

	packages, err := monitor.binaries()
	if err != nil {
		t.Fatalf("binaries should not error for empty goBin: %v", err)
	}

	if packages != nil {
		t.Errorf("Expected nil packages, got %v", packages)
	}
}

func TestGoGetInstalledPackages(t *testing.T) {
	tmpDir := t.TempDir()
	monitor := initializedGoMonitor(t, tmpDir)
	executablePath := filepath.Join(tmpDir, "mytool")
	writeMonitorExecutable(t, executablePath, []byte("#!/bin/bash"))

	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}

	if packages == nil {
		t.Fatal("Expected non-nil packages")
	}
}
