package monitors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

type ProcessMonitor struct {
	*BaseMonitor
	binaryPath   string
	wrapperPath  string
	originalPath string
}

func NewProcessMonitor(name, binaryPath string) *ProcessMonitor {
	return &ProcessMonitor{
		BaseMonitor: NewBaseMonitor(name),
		binaryPath:  binaryPath,
	}
}

func (m *ProcessMonitor) Initialize(config *core.Config) error {
	if err := m.BaseMonitor.Initialize(config); err != nil {
		return err
	}

	m.wrapperPath = filepath.Join(config.Monitoring.Process.WrapperDir, m.name)
	m.originalPath = m.findOriginalBinary()

	if config.Monitoring.Process.AutoInstallWrappers {
		return m.InstallWrapper()
	}

	return nil
}

func (m *ProcessMonitor) findOriginalBinary() string {
	paths := strings.Split(os.Getenv("PATH"), ":")
	for _, path := range paths {
		if path == m.config.Monitoring.Process.WrapperDir {
			continue
		}

		candidate := filepath.Join(path, filepath.Base(m.binaryPath))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if info.Mode()&0111 != 0 {
				return candidate
			}
		}
	}
	return m.binaryPath
}

func (m *ProcessMonitor) InstallWrapper() error {
	if err := os.MkdirAll(m.config.Monitoring.Process.WrapperDir, 0755); err != nil {
		return fmt.Errorf("failed to create wrapper directory: %w", err)
	}

	wrapperContent := m.generateWrapperScript()
	if err := os.WriteFile(m.wrapperPath, []byte(wrapperContent), 0755); err != nil {
		return fmt.Errorf("failed to write wrapper script: %w", err)
	}

	return m.updateShellConfig()
}

func (m *ProcessMonitor) generateWrapperScript() string {
	return fmt.Sprintf(`#!/bin/bash
DIU_SOCKET="%s"
ORIGINAL_BINARY="%s"
START_TIME=$(date +%%s%%N)

"$ORIGINAL_BINARY" "$@"
EXIT_CODE=$?

END_TIME=$(date +%%s%%N)
DURATION=$((($END_TIME - $START_TIME) / 1000000))

if [ -S "$DIU_SOCKET" ]; then
    echo "{
        \"tool\": \"%s\",
        \"command\": \"$ORIGINAL_BINARY $*\",
        \"args\": \"$@\",
        \"exit_code\": $EXIT_CODE,
        \"duration_ms\": $DURATION,
        \"timestamp\": \"$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)\",
        \"working_dir\": \"$(pwd)\",
        \"user\": \"$(whoami)\"
    }" | nc -U "$DIU_SOCKET" 2>/dev/null || true
fi

exit $EXIT_CODE
`, core.DefaultSocketPath, m.originalPath, m.name)
}

func (m *ProcessMonitor) updateShellConfig() error {
	usr, _ := user.Current()
	homeDir := usr.HomeDir

	shellConfigs := []string{
		filepath.Join(homeDir, ".bashrc"),
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".config", "fish", "config.fish"),
	}

	exportLine := fmt.Sprintf("export PATH=\"%s:$PATH\"", m.config.Monitoring.Process.WrapperDir)

	for _, configFile := range shellConfigs {
		if _, err := os.Stat(configFile); err == nil {
			content, err := os.ReadFile(configFile)
			if err != nil {
				continue
			}

			if !strings.Contains(string(content), exportLine) {
				file, err := os.OpenFile(configFile, os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					continue
				}
				defer file.Close()

				file.WriteString("\n# DIU path configuration\n")
				file.WriteString(exportLine + "\n")
			}
		}
	}

	return nil
}

func (m *ProcessMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	return nil
}

func (m *ProcessMonitor) ExecuteAndTrack(cmd string, args []string) (*core.ExecutionRecord, error) {
	startTime := time.Now()

	command := exec.Command(m.originalPath, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin

	err := command.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	duration := time.Since(startTime)
	workingDir, _ := os.Getwd()
	usr, _ := user.Current()

	record := &core.ExecutionRecord{
		ID:         fmt.Sprintf("exec_%s_%d", time.Now().Format("20060102_150405"), time.Now().UnixNano()),
		Tool:       m.name,
		Command:    fmt.Sprintf("%s %s", cmd, strings.Join(args, " ")),
		Args:       args,
		Timestamp:  startTime,
		Duration:   duration,
		ExitCode:   exitCode,
		WorkingDir: workingDir,
		User:       usr.Username,
	}

	if parsed, err := m.ParseCommand(cmd, args); err == nil {
		record.PackagesAffected = parsed.PackagesAffected
		record.Metadata = parsed.Metadata
	}

	return record, nil
}

func (m *ProcessMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	return nil, fmt.Errorf("not implemented for base process monitor")
}

func (m *ProcessMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return &core.ExecutionRecord{
		Tool:    m.name,
		Command: cmd,
		Args:    args,
	}, nil
}

func CreateWrapperScript(tool, originalPath, wrapperDir string) string {
	return fmt.Sprintf(`#!/bin/bash
# DIU wrapper for %s
DIU_DAEMON_URL="http://localhost:8080/api/v1/executions"
ORIGINAL="%s"
START_TIME=$(date +%%s%%N)

# Execute original command
"$ORIGINAL" "$@"
EXIT_CODE=$?

END_TIME=$(date +%%s%%N)
DURATION=$((($END_TIME - $START_TIME) / 1000000))

# Send to DIU daemon (non-blocking)
{
    curl -X POST "$DIU_DAEMON_URL" \
        -H "Content-Type: application/json" \
        -d "{
            \"tool\": \"%s\",
            \"command\": \"$ORIGINAL $*\",
            \"args\": $(printf '%%s\n' "$@" | jq -R . | jq -s .),
            \"exit_code\": $EXIT_CODE,
            \"duration_ms\": $DURATION,
            \"timestamp\": \"$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)\",
            \"working_dir\": \"$(pwd)\",
            \"user\": \"$(whoami)\"
        }" 2>/dev/null
} &

exit $EXIT_CODE
`, tool, originalPath, tool)
}