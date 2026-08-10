//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

var (
	apiURL = getAPIURL()
)

func getAPIURL() string {
	if url := os.Getenv("DIU_API_URL"); url != "" {
		return url
	}
	return fmt.Sprintf("http://%s:%d", core.DefaultAPIHost, core.DefaultAPIPort)
}

func TestE2EDaemonHealth(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}

	// Wait for daemon to be ready
	var resp *http.Response
	var err error
	for i := 0; i < 30; i++ {
		resp, err = client.Get(apiURL + "/api/v1/health")
		if err == nil && resp.StatusCode == 200 {
			break
		}
		time.Sleep(time.Second)
	}

	if err != nil {
		t.Fatalf("Failed to connect to daemon: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("Failed to decode health response: %v", err)
	}

	if health["status"] != "healthy" {
		t.Errorf("Expected healthy status, got %v", health["status"])
	}
}

func TestE2EExecutionTracking(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}

	// Send a test execution
	execution := map[string]interface{}{
		"tool":              "test",
		"command":           "test command",
		"args":              []string{"arg1", "arg2"},
		"exit_code":         0,
		"duration_ms":       100,
		"timestamp":         time.Now().Format(time.RFC3339),
		"working_dir":       "/tmp",
		"user":              "e2e-test",
		"packages_affected": []string{"test-package"},
	}

	jsonData, _ := json.Marshal(execution)
	resp, err := client.Post(
		apiURL+"/api/v1/executions",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		t.Fatalf("Failed to post execution: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 202 {
		t.Errorf("Expected status 202, got %d", resp.StatusCode)
	}

	// Wait a moment for processing
	time.Sleep(time.Second)

	// Query the execution
	resp, err = client.Get(apiURL + "/api/v1/executions?tool=test")
	if err != nil {
		t.Fatalf("Failed to query executions: %v", err)
	}
	defer resp.Body.Close()

	var executions []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
		t.Fatalf("Failed to decode executions: %v", err)
	}

	found := false
	for _, exec := range executions {
		if exec["tool"] == "test" && exec["user"] == "e2e-test" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Test execution not found in query results")
	}
}

func TestE2EPackageTracking(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}

	execution := map[string]interface{}{
		"tool":              core.ToolNPM,
		"command":           "npm install express",
		"args":              []string{"install", "express"},
		"exit_code":         0,
		"duration_ms":       5000,
		"timestamp":         time.Now().Format(time.RFC3339),
		"working_dir":       "/tmp",
		"user":              "e2e-test",
		"packages_affected": []string{"express"},
		"metadata":          map[string]interface{}{"global": true},
	}

	jsonData, _ := json.Marshal(execution)
	client.Post(
		apiURL+"/api/v1/executions",
		"application/json",
		bytes.NewBuffer(jsonData),
	)

	// Wait for processing
	time.Sleep(2 * time.Second)

	resp, err := client.Get(apiURL + "/api/v1/packages?tool=" + core.ToolNPM)
	if err != nil {
		t.Fatalf("Failed to query packages: %v", err)
	}
	defer resp.Body.Close()

	var packages []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&packages); err != nil {
		t.Fatalf("Failed to decode packages: %v", err)
	}

	found := false
	for _, pkg := range packages {
		if pkg["name"] == "express" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Package 'express' not found in tracked packages")
	}
}

func TestE2EStatistics(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}

	tools := []string{core.ToolHomebrew, core.ToolNPM, core.ToolGo}
	for _, tool := range tools {
		execution := map[string]interface{}{
			"tool":        tool,
			"command":     fmt.Sprintf("%s test", tool),
			"args":        []string{"test"},
			"exit_code":   0,
			"duration_ms": 100,
			"timestamp":   time.Now().Format(time.RFC3339),
			"working_dir": "/tmp",
			"user":        "e2e-test",
		}

		jsonData, _ := json.Marshal(execution)
		client.Post(
			apiURL+"/api/v1/executions",
			"application/json",
			bytes.NewBuffer(jsonData),
		)
	}

	// Wait for processing
	time.Sleep(2 * time.Second)

	// Get statistics
	resp, err := client.Get(apiURL + "/api/v1/stats")
	if err != nil {
		t.Fatalf("Failed to get statistics: %v", err)
	}
	defer resp.Body.Close()

	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("Failed to decode statistics: %v", err)
	}

	if stats["total_executions"] == nil {
		t.Error("Statistics missing total_executions")
	}

	if stats["execution_frequency"] == nil {
		t.Error("Statistics missing execution_frequency")
	}
}

func TestE2ECLICommands(t *testing.T) {
	// Skip if not in Docker environment
	if _, err := os.Stat("/usr/local/bin/diu"); os.IsNotExist(err) {
		t.Skip("DIU binary not available, skipping CLI tests")
	}

	tests := []struct {
		name          string
		args          []string
		expectSuccess bool
	}{
		{
			name:          "daemon status",
			args:          []string{"daemon", "status"},
			expectSuccess: true,
		},
		{
			name:          "query executions",
			args:          []string{"query", "--limit", "10"},
			expectSuccess: true,
		},
		{
			name:          "show stats",
			args:          []string{"stats"},
			expectSuccess: true,
		},
		{
			name:          "list packages",
			args:          []string{"packages"},
			expectSuccess: true,
		},
		{
			name:          "config list",
			args:          []string{"config", "list"},
			expectSuccess: true,
		},
		{
			name:          "diagnostics",
			args:          []string{"diagnostics"},
			expectSuccess: true,
		},
		{
			name:          "status",
			args:          []string{"status"},
			expectSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("diu", tt.args...)
			output, err := cmd.CombinedOutput()

			if tt.expectSuccess && err != nil {
				t.Errorf("Command failed: %v\nOutput: %s", err, output)
			}
			if !tt.expectSuccess && err == nil {
				t.Errorf("Expected command to fail but it succeeded\nOutput: %s", output)
			}
		})
	}
}

func TestE2EFallbackRecordersDoNotQueue(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/diu"); os.IsNotExist(err) {
		t.Skip("DIU binary not available, skipping fallback recorder test")
	}
	homeDir := t.TempDir()
	storagePath := filepath.Join(homeDir, ".local", "share", "diu", "executions.json")
	if err := os.MkdirAll(filepath.Dir(storagePath), core.OwnerDirectoryMode); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	lock := openFallbackLock(t, storagePath+".fallback.lock")
	defer closeFallbackLock(t, lock)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := `{"tool":"npm","command":"npm install eslint"}`
	commands := startFallbackRecorders(t, ctx, homeDir, payload, 24)
	for _, command := range commands {
		if err := command.cmd.Wait(); err == nil {
			t.Fatalf("contended fallback recorder unexpectedly succeeded: %s", command.output.String())
		}
	}
	if _, err := os.Stat(storagePath); !os.IsNotExist(err) {
		t.Fatalf("contended fallback recorders touched storage: %v", err)
	}
	markerPath := filepath.Join(filepath.Dir(storagePath), "fallback-contention")
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("fallback contention marker missing: %v", err)
	}
}

func openFallbackLock(t *testing.T, path string) *os.File {
	t.Helper()
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		t.Fatalf("Flock failed: %v", err)
	}
	return lock
}

func closeFallbackLock(t *testing.T, lock *os.File) {
	t.Helper()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Errorf("unlock failed: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Errorf("close failed: %v", err)
	}
}

type fallbackRecorder struct {
	cmd    *exec.Cmd
	output *bytes.Buffer
}

func startFallbackRecorders(
	t *testing.T,
	ctx context.Context,
	homeDir string,
	payload string,
	count int,
) []fallbackRecorder {
	t.Helper()
	commands := make([]fallbackRecorder, 0, count)
	for range count {
		command := exec.CommandContext(ctx, "diu", "record")
		command.Env = environmentWithHome(homeDir)
		command.Stdin = strings.NewReader(payload)
		output := &bytes.Buffer{}
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			t.Fatalf("failed to start fallback recorder: %v", err)
		}
		commands = append(commands, fallbackRecorder{cmd: command, output: output})
	}
	return commands
}

func environmentWithHome(homeDir string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "HOME=") {
			environment = append(environment, value)
		}
	}
	return append(environment, "HOME="+homeDir)
}

func TestE2EWrapperExecution(t *testing.T) {
	// This test simulates wrapper script execution
	client := &http.Client{Timeout: 10 * time.Second}

	// Simulate wrapper sending execution data
	wrapperData := map[string]interface{}{
		"tool":              "brew",
		"command":           "brew install wget",
		"args":              []string{"install", "wget"},
		"exit_code":         0,
		"duration_ms":       45230,
		"timestamp":         time.Now().Format(time.RFC3339),
		"working_dir":       "/Users/test/projects",
		"user":              "wrapper-test",
		"packages_affected": []string{"wget"},
		"metadata": map[string]interface{}{
			"brew_version":     "4.1.15",
			"formulae_updated": []string{"wget"},
		},
	}

	jsonData, _ := json.Marshal(wrapperData)
	resp, err := client.Post(
		apiURL+"/api/v1/executions",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		t.Fatalf("Failed to send wrapper data: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 202 {
		t.Errorf("Expected status 202, got %d", resp.StatusCode)
	}

	// Verify the execution was recorded
	time.Sleep(time.Second)

	resp, err = client.Get(apiURL + "/api/v1/executions?tool=brew&limit=1")
	if err != nil {
		t.Fatalf("Failed to query executions: %v", err)
	}
	defer resp.Body.Close()

	var executions []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
		t.Fatalf("Failed to decode executions: %v", err)
	}

	if len(executions) == 0 {
		t.Fatal("No executions found after wrapper simulation")
	}

	exec := executions[0]
	if exec["user"] != "wrapper-test" {
		t.Errorf("Expected user 'wrapper-test', got %v", exec["user"])
	}

	if packages, ok := exec["packages_affected"].([]interface{}); ok {
		if len(packages) == 0 || packages[0] != "wget" {
			t.Error("Package 'wget' not properly recorded")
		}
	}
}
