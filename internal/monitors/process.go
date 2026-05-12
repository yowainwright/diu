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
	"github.com/yowainwright/diu/internal/safefs"
)

type ProcessMonitor struct {
	*BaseMonitor
	binaryPath   string
	wrapperPath  string
	originalPath string
	homeDir      string
}

func NewProcessMonitor(name, binaryPath string) *ProcessMonitor {
	homeDir := os.Getenv("HOME")
	if usr, err := user.Current(); err == nil {
		homeDir = usr.HomeDir
	}
	return &ProcessMonitor{
		BaseMonitor: NewBaseMonitor(name),
		binaryPath:  binaryPath,
		homeDir:     homeDir,
	}
}

func (m *ProcessMonitor) Initialize(config *core.Config) error {
	if err := m.BaseMonitor.Initialize(config); err != nil {
		return err
	}

	m.wrapperPath = filepath.Join(config.Monitoring.Process.WrapperDir, filepath.Base(m.binaryPath))
	m.originalPath = m.findOriginalBinary()

	if config.Monitoring.Process.AutoInstallWrappers {
		return m.InstallWrapper()
	}

	return nil
}

func (m *ProcessMonitor) findOriginalBinary() string {
	paths := filepath.SplitList(os.Getenv("PATH"))
	wrapperDir := filepath.Clean(m.config.Monitoring.Process.WrapperDir)
	for _, path := range paths {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) == wrapperDir {
			continue
		}

		candidate := filepath.Join(path, filepath.Base(m.binaryPath))
		if info, err := safefs.Stat(candidate); err == nil && !info.IsDir() {
			if info.Mode()&core.ExecutableModeMask != 0 {
				return candidate
			}
		}
	}
	return m.binaryPath
}

func (m *ProcessMonitor) InstallWrapper() error {
	if err := os.MkdirAll(m.config.Monitoring.Process.WrapperDir, core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create wrapper directory: %w", err)
	}

	wrapperContent := m.generateWrapperScript()
	if err := writeOwnerExecutableFile(m.wrapperPath, []byte(wrapperContent)); err != nil {
		return fmt.Errorf("failed to write wrapper script: %w", err)
	}

	return m.updateShellConfig()
}

func writeOwnerExecutableFile(path string, data []byte) (err error) {
	file, err := safefs.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Chmod(core.OwnerExecutableMode)
}

func (m *ProcessMonitor) generateWrapperScript() string {
	apiEndpoint := fmt.Sprintf("http://%s:%d/api/v1/executions", m.config.API.Host, m.config.API.Port)
	return fmt.Sprintf(`#!/bin/bash
ORIGINAL="%s"
DIU_API="%s"
DIU_TOOL="%s"
START_TIME=$(date +%%s)
WORKING_DIR=$(pwd)

"$ORIGINAL" "$@"
EXIT_CODE=$?

END_TIME=$(date +%%s)
DURATION=$(( (END_TIME - START_TIME) * 1000 ))

build_json() {
    local args_json="["
    local first=true
    for arg in "$@"; do
        if [ "$first" = true ]; then
            first=false
        else
            args_json="$args_json,"
        fi
        escaped_arg=$(echo "$arg" | sed 's/\\/\\\\/g' | sed 's/"/\\"/g')
        args_json="$args_json\"$escaped_arg\""
    done
    args_json="$args_json]"
    escaped_cmd=$(printf '%%s %%s' "$ORIGINAL" "$*" | sed 's/\\/\\\\/g' | sed 's/"/\\"/g' | tr -d '\n')
    escaped_dir=$(printf '%%s' "$WORKING_DIR" | sed 's/\\/\\\\/g' | sed 's/"/\\"/g' | tr -d '\n')

    cat <<EOF
{
    "tool": "$DIU_TOOL",
    "command": "$escaped_cmd",
    "args": $args_json,
    "exit_code": $EXIT_CODE,
    "duration_ms": $DURATION,
    "timestamp": "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)",
    "working_dir": "$escaped_dir",
    "user": "$(whoami)"
}
EOF
}

{
    if command -v curl >/dev/null 2>&1; then
        build_json "$@" | curl -X POST "$DIU_API" \
            -H "Content-Type: application/json" \
            -d @- \
            --silent \
            --fail \
            --connect-timeout 1 \
            --max-time 2 \
            2>/dev/null
    fi
} &

exit $EXIT_CODE
`, core.ShellEscapeString(m.originalPath), apiEndpoint, m.name)
}

func (m *ProcessMonitor) updateShellConfig() error {
	shellConfigs := []string{
		filepath.Join(m.homeDir, ".bashrc"),
		filepath.Join(m.homeDir, ".zshrc"),
		filepath.Join(m.homeDir, ".config", "fish", "config.fish"),
	}

	exportLine := fmt.Sprintf("export PATH=\"%s:$PATH\"", m.config.Monitoring.Process.WrapperDir)

	for _, configFile := range shellConfigs {
		if _, err := safefs.Stat(configFile); err == nil {
			content, err := safefs.ReadFile(configFile)
			if err != nil {
				continue
			}

			if !strings.Contains(string(content), exportLine) {
				if err := appendShellConfigLines(configFile, "\n# DIU path configuration\n", exportLine+"\n"); err != nil {
					continue
				}
			}
		}
	}

	return nil
}

func appendShellConfigLines(path string, lines ...string) (err error) {
	file, err := safefs.OpenFile(path, os.O_APPEND|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	for _, line := range lines {
		if _, err := file.WriteString(line); err != nil {
			return err
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

	originalPath, err := validateExecutablePath(m.originalPath)
	if err != nil {
		return nil, err
	}

	// #nosec G204 -- originalPath is resolved from PATH, validated as an absolute executable, and args are forwarded intentionally.
	command := exec.Command(originalPath, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin

	err = command.Run()
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

func validateExecutablePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("executable path cannot be empty")
	}

	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return "", fmt.Errorf("executable path must be absolute: %s", path)
	}

	info, err := safefs.Stat(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to inspect executable %s: %w", cleanPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("executable path is a directory: %s", cleanPath)
	}
	if info.Mode()&core.ExecutableModeMask == 0 {
		return "", fmt.Errorf("executable path is not executable: %s", cleanPath)
	}

	return cleanPath, nil
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
DIU_DAEMON_URL="http://localhost:8081/api/v1/executions"
ORIGINAL="%s"
START_TIME=$(date +%%s)

# Execute original command
"$ORIGINAL" "$@"
EXIT_CODE=$?

END_TIME=$(date +%%s)
DURATION=$((($END_TIME - $START_TIME) * 1000))

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
