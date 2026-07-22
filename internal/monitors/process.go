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
	originalPath, err := m.findOriginalBinary()
	if err != nil {
		if config.Monitoring.Process.AutoInstallWrappers {
			return err
		}
		m.originalPath = m.binaryPath
	} else {
		m.originalPath = originalPath
	}

	if config.Monitoring.Process.AutoInstallWrappers {
		return m.InstallWrapper()
	}

	return nil
}

func (m *ProcessMonitor) findOriginalBinary() (string, error) {
	if filepath.IsAbs(m.binaryPath) {
		validatedPath, err := validateExecutablePath(m.binaryPath)
		if err != nil {
			return "", err
		}
		if pathWithinDirectory(validatedPath, m.config.Monitoring.Process.WrapperDir) {
			return "", fmt.Errorf("original binary %q resolves inside wrapper directory %s", validatedPath, filepath.Clean(m.config.Monitoring.Process.WrapperDir))
		}
		return validatedPath, nil
	}

	paths := filepath.SplitList(os.Getenv("PATH"))
	wrapperDir := filepath.Clean(m.config.Monitoring.Process.WrapperDir)
	for _, path := range paths {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) == wrapperDir {
			continue
		}

		candidate := filepath.Join(path, filepath.Base(m.binaryPath))
		if info, err := safefs.Stat(candidate); err == nil && !info.IsDir() {
			if info.Mode()&core.ExecutableModeMask != 0 {
				validatedPath, err := validateExecutablePath(candidate)
				if err != nil {
					continue
				}
				if pathWithinDirectory(validatedPath, wrapperDir) {
					continue
				}
				return validatedPath, nil
			}
		}
	}
	return "", fmt.Errorf("original binary %q not found in PATH outside wrapper directory %s", filepath.Base(m.binaryPath), wrapperDir)
}

func pathWithinDirectory(path, dir string) bool {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(dir) == "" {
		return false
	}

	cleanPath := filepath.Clean(path)
	cleanDir := filepath.Clean(dir)
	if !filepath.IsAbs(cleanPath) {
		absPath, err := filepath.Abs(cleanPath)
		if err != nil {
			return false
		}
		cleanPath = absPath
	}
	if !filepath.IsAbs(cleanDir) {
		absDir, err := filepath.Abs(cleanDir)
		if err != nil {
			return false
		}
		cleanDir = absDir
	}
	if resolvedPath, err := filepath.EvalSymlinks(cleanPath); err == nil {
		cleanPath = resolvedPath
	}
	if resolvedDir, err := filepath.EvalSymlinks(cleanDir); err == nil {
		cleanDir = resolvedDir
	}

	relativePath, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil {
		return false
	}
	return relativePath == "." || (relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)))
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
	return generateProcessWrapperScript(m.originalPath, "diu", m.config.Daemon.SocketPath, m.name)
}

func generateProcessWrapperScript(originalPath, diuPath, socketPath, tool string) string {
	return fmt.Sprintf(`#!/bin/bash
%s
ORIGINAL="%s"
DIU_BINARY="%s"
DIU_SOCKET="%s"
DIU_TOOL="%s"
START_TIME=$(date +%%s)

"$ORIGINAL" "$@"
EXIT_CODE=$?

END_TIME=$(date +%%s)
DURATION=$(( (END_TIME - START_TIME) * 1000 ))

json_escape() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//$'\n'/\\n}"
    value="${value//$'\r'/\\r}"
    value="${value//$'\t'/\\t}"
    printf '%%s' "$value"
}

args_json="["
first=true
for arg in "$@"; do
    if [ "$first" = true ]; then
        first=false
    else
        args_json="$args_json,"
    fi
    args_json="$args_json\"$(json_escape "$arg")\""
done
args_json="$args_json]"

payload=$(cat <<EOF
{
    "tool": "$DIU_TOOL",
    "command": "$(json_escape "$DIU_TOOL $*")",
    "args": $args_json,
    "exit_code": $EXIT_CODE,
    "duration_ms": $DURATION,
    "timestamp": "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)",
    "working_dir": "$(json_escape "$(pwd)")",
    "user": "$(json_escape "$(whoami)")",
    "metadata": {
        "original_path": "$(json_escape "$ORIGINAL")"
    }
}
EOF
)

{
    sent=false
    if [ -S "$DIU_SOCKET" ] && command -v nc >/dev/null 2>&1; then
        if printf '%%s\n' "$payload" | nc -w 1 -U "$DIU_SOCKET" 2>/dev/null; then
            sent=true
        fi
    fi

    if [ "$sent" != true ]; then
        DIU_RECORD_BINARY="$(command -v "$DIU_BINARY" 2>/dev/null || true)"
        if [ -n "$DIU_RECORD_BINARY" ] && [ -x "$DIU_RECORD_BINARY" ]; then
            printf '%%s\n' "$payload" | "$DIU_RECORD_BINARY" record >/dev/null 2>&1
        fi
    fi
} &>/dev/null &

exit $EXIT_CODE
`, core.GeneratedWrapperMarker, core.ShellEscapeString(originalPath), core.ShellEscapeString(diuPath), core.ShellEscapeString(socketPath), core.ShellEscapeString(tool))
}

func (m *ProcessMonitor) updateShellConfig() error {
	wrapperDir := m.config.Monitoring.Process.WrapperDir
	bashPath := filepath.Join(m.homeDir, ".bashrc")
	zshPath := filepath.Join(m.homeDir, ".zshrc")
	fishConfigDir := filepath.Join(m.homeDir, ".config", "fish")
	fishPath := filepath.Join(fishConfigDir, "config.fish")
	posixLine := core.PosixPathLine(wrapperDir)
	fishLine := core.FishPathLine(wrapperDir)

	appendPathConfigIfPresent(bashPath, posixLine)
	appendPathConfigIfPresent(zshPath, posixLine)
	appendPathConfigIfPresent(fishPath, fishLine)

	return nil
}

func appendPathConfigIfPresent(path, line string) {
	if _, err := safefs.Stat(path); err != nil {
		return
	}
	content, err := safefs.ReadFile(path)
	if err != nil {
		return
	}
	contentText := string(content)
	if strings.Contains(contentText, line) {
		return
	}
	lineWithNewline := line + "\n"
	_ = appendShellConfigLines(path, "\n"+core.ShellPathMarker+"\n", lineWithNewline)
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

	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve executable %s: %w", cleanPath, err)
	}

	info, err := safefs.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to inspect executable %s: %w", resolvedPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("executable path is a directory: %s", resolvedPath)
	}
	if info.Mode()&core.ExecutableModeMask == 0 {
		return "", fmt.Errorf("executable path is not executable: %s", resolvedPath)
	}

	return resolvedPath, nil
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
