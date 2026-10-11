package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestGetConfig(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := getConfig(&command{}, []string{"storage.retention_days"}); err != nil {
			t.Fatalf("getConfig failed: %v", err)
		}
	})

	if strings.TrimSpace(output) != fmt.Sprintf("%d", core.DefaultRetentionDays) {
		t.Fatalf("Expected retention_days value, got: %q", output)
	}
}

func TestGetConfigUnknownKey(t *testing.T) {
	setupTestHomeConfig(t)

	err := getConfig(&command{}, []string{"unknown.key"})
	if err == nil {
		t.Fatal("Expected error for unknown config key")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("Expected 'unknown config key' error, got: %v", err)
	}
}

func TestGetConfigNoKey(t *testing.T) {
	err := getConfig(&command{}, []string{})
	if err == nil {
		t.Fatal("Expected error for missing config key")
	}
	if !strings.Contains(err.Error(), "config key required") {
		t.Fatalf("Expected 'config key required' error, got: %v", err)
	}
}

func TestSetConfig(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStderr(t, func() {
		if err := setConfig(&command{}, []string{"storage.retention_days", "30"}); err != nil {
			t.Fatalf("setConfig failed: %v", err)
		}
	})

	if !strings.Contains(output, "Configuration updated") {
		t.Fatalf("Expected 'Configuration updated', got: %q", output)
	}
	getOutput := captureStdout(t, func() {
		if err := getConfig(&command{}, []string{"storage.retention_days"}); err != nil {
			t.Fatalf("getConfig after set failed: %v", err)
		}
	})
	if strings.TrimSpace(getOutput) != "30" {
		t.Fatalf("Expected '30', got: %q", getOutput)
	}
}

func TestSetConfigInvalidValue(t *testing.T) {
	err := setConfig(&command{}, []string{"storage.retention_days", "invalid"})
	if err == nil {
		t.Fatal("Expected error for invalid retention_days value")
	}
	if !strings.Contains(err.Error(), "invalid retention_days value") {
		t.Fatalf("Expected 'invalid retention_days value' error, got: %v", err)
	}
}

func TestSetConfigNegativeValue(t *testing.T) {
	err := setConfig(&command{}, []string{"storage.retention_days", "-1"})
	if err == nil {
		t.Fatal("Expected error for negative retention_days value")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("Expected 'non-negative' error, got: %v", err)
	}
}

func TestSetConfigNoArgs(t *testing.T) {
	err := setConfig(&command{}, []string{})
	if err == nil {
		t.Fatal("Expected error for missing args")
	}
	if !strings.Contains(err.Error(), "config key and value required") {
		t.Fatalf("Expected 'config key and value required' error, got: %v", err)
	}
}

func TestListConfig(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := listConfig(&command{}, nil); err != nil {
			t.Fatalf("listConfig failed: %v", err)
		}
	})

	var listed core.Config
	if err := json.Unmarshal([]byte(output), &listed); err != nil {
		t.Fatalf("Failed to decode config JSON: %v", err)
	}
	if listed.Version != config.Version {
		t.Fatalf("Expected version %q, got %q", config.Version, listed.Version)
	}
}

func TestGetConfigAllKeys(t *testing.T) {
	setupTestHomeConfig(t)
	validKeys := []string{
		"storage.json_file",
		"storage.retention_days",
		"storage.max_executions",
		"storage.max_storage_bytes",
		"storage.max_backups",
		"daemon.pid_file",
		"daemon.socket_path",
		"api.enabled",
		"api.port",
		"monitoring.enabled_tools",
	}

	for _, key := range validKeys {
		t.Run(key, func(t *testing.T) {
			output := captureStdout(t, func() {
				if err := getConfig(&command{}, []string{key}); err != nil {
					t.Fatalf("getConfig(%s) failed: %v", key, err)
				}
			})
			trimmed := strings.TrimSpace(output)
			if key == "monitoring.enabled_tools" {
				return
			}
			if trimmed == "" {
				t.Fatalf("getConfig(%s) returned empty output", key)
			}
		})
	}
}

func TestSetConfigAllKeys(t *testing.T) {
	setupTestHomeConfig(t)

	for _, tt := range setConfigCases {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			assertSetConfigCase(t, tt)
		})
	}
}

type setConfigCase struct {
	key   string
	value string
}

var setConfigCases = []setConfigCase{
	{"storage.retention_days", "7"},
	{"storage.max_executions", "500"},
	{"storage.max_storage_bytes", "1073741824"},
	{"storage.max_backups", "5"},
	{"daemon.pid_file", "/tmp/diu.pid"},
	{"daemon.socket_path", "/tmp/diu.sock"},
	{"api.enabled", "false"},
	{"api.port", "9090"},
	{"monitoring.enabled_tools", "homebrew,npm"},
}

func assertSetConfigCase(t *testing.T, test setConfigCase) {
	t.Helper()

	output := captureStderr(t, func() {
		if err := setConfig(&command{}, []string{test.key, test.value}); err != nil {
			t.Fatalf("setConfig(%s, %s) failed: %v", test.key, test.value, err)
		}
	})
	if !strings.Contains(output, "Configuration updated") {
		t.Fatalf("Expected 'Configuration updated', got: %q", output)
	}
	assertConfigValue(t, test)
}

func assertConfigValue(t *testing.T, test setConfigCase) {
	t.Helper()

	getOutput := captureStdout(t, func() {
		if err := getConfig(&command{}, []string{test.key}); err != nil {
			t.Fatalf("getConfig(%s) failed: %v", test.key, err)
		}
	})
	expectedOutput := expectedConfigOutput(test)
	if strings.TrimSpace(getOutput) != expectedOutput {
		t.Fatalf("getConfig(%s) = %q, want %q", test.key, strings.TrimSpace(getOutput), expectedOutput)
	}
}

func expectedConfigOutput(test setConfigCase) string {
	if test.key == "monitoring.enabled_tools" {
		return strings.ReplaceAll(test.value, ",", ", ")
	}
	return test.value
}

func assertConfigSetGetAndList(t *testing.T) {
	t.Helper()

	setOutput := captureStderr(t, func() {
		if err := setConfig(&command{}, []string{"storage.retention_days", "30"}); err != nil {
			t.Fatalf("setConfig failed: %v", err)
		}
	})
	if !strings.Contains(setOutput, "Configuration updated") {
		t.Fatalf("Unexpected setConfig output: %q", setOutput)
	}

	getOutput := captureStdout(t, func() {
		if err := getConfig(&command{}, []string{"storage.retention_days"}); err != nil {
			t.Fatalf("getConfig failed: %v", err)
		}
	})
	if strings.TrimSpace(getOutput) != "30" {
		t.Fatalf("retention_days = %q, want 30", getOutput)
	}

	listOutput := captureStdout(t, func() {
		if err := listConfig(&command{}, nil); err != nil {
			t.Fatalf("listConfig failed: %v", err)
		}
	})
	var listed core.Config
	if err := json.Unmarshal([]byte(listOutput), &listed); err != nil {
		t.Fatalf("Failed to decode config list output: %v", err)
	}
	if listed.Storage.RetentionDays != 30 {
		t.Fatalf("Listed retention_days = %d, want 30", listed.Storage.RetentionDays)
	}
}
