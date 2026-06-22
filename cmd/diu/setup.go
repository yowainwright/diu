package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

// executableWrapper represents an executable to be wrapped
type executableWrapper struct {
	Name         string
	OriginalPath string
	Tool         string
	Package      string
}

// setupProject initializes DIU storage and wrappers
func setupProject(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := config.EnsureDirectories(); err != nil {
		return err
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to initialize storage: %w", err)
	}
	if err := store.Close(); err != nil {
		return fmt.Errorf("failed to close storage: %w", err)
	}

	if err := installWrappers(config); err != nil {
		return err
	}
	if err := installExecutableWrappers(config); err != nil {
		return err
	}

	fmt.Println(successStyle.Render("DIU setup completed"))
	return nil
}

// scanPackages scans for installed packages
func scanPackages(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	scanConfig := *config
	scanConfig.Monitoring.Process.AutoInstallWrappers = false

	total := 0
	for _, tool := range scanConfig.Monitoring.EnabledTools {
		monitor, err := newMonitor(core.NormalizeToolName(tool))
		if err != nil {
			continue
		}
		if err := monitor.Initialize(&scanConfig); err != nil {
			fmt.Printf("Warning: failed to initialize %s monitor: %v\n", tool, err)
			continue
		}

		packages, err := monitor.GetInstalledPackages()
		if err != nil {
			fmt.Printf("Warning: failed to scan %s packages: %v\n", tool, err)
			continue
		}

		for _, pkg := range packages {
			if existing, err := store.GetPackage(pkg.Tool, pkg.Name); err == nil {
				pkg.LastUsed = existing.LastUsed
				pkg.UsageCount = existing.UsageCount
			}
			if err := store.UpdatePackage(pkg); err != nil {
				return fmt.Errorf("failed to update package %s/%s: %w", pkg.Tool, pkg.Name, err)
			}
			total++
		}
	}
	seenExecutables := make(map[string]bool)
	for _, target := range discoverExecutableWrappers(config) {
		key := target.Tool + "/" + target.Package
		if seenExecutables[key] || target.Package == "" {
			continue
		}
		seenExecutables[key] = true

		pkg := &core.PackageInfo{
			Name:        target.Package,
			Tool:        target.Tool,
			InstallDate: time.Now(),
			Path:        target.OriginalPath,
		}
		if existing, err := store.GetPackage(pkg.Tool, pkg.Name); err == nil {
			pkg.Version = existing.Version
			pkg.InstallDate = existing.InstallDate
			pkg.LastUsed = existing.LastUsed
			pkg.UsageCount = existing.UsageCount
			if existing.Path != "" {
				pkg.Path = existing.Path
			}
		}
		if err := store.UpdatePackage(pkg); err != nil {
			return fmt.Errorf("failed to update executable package %s/%s: %w", pkg.Tool, pkg.Name, err)
		}
		total++
	}

	fmt.Printf("%s\n", successStyle.Render(fmt.Sprintf("%d packages scanned", total)))
	return nil
}

// cleanup cleans up old execution records
func cleanup(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	if err := store.Cleanup(time.Time{}); err != nil {
		return fmt.Errorf("cleanup failed: %w", err)
	}

	fmt.Println(successStyle.Render("Cleanup completed"))
	return nil
}

// backup creates a manual backup
func backup(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	if err := store.Backup(); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	fmt.Println(successStyle.Render("Backup created"))
	return nil
}

// recordExecution records an execution event from stdin
func recordExecution(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	var record core.ExecutionRecord
	if err := json.NewDecoder(os.Stdin).Decode(&record); err != nil {
		return fmt.Errorf("failed to decode execution record: %w", err)
	}

	enrichExecutionRecord(config, &record)

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer closeStore(store)

	if err := store.AddExecution(&record); err != nil {
		return fmt.Errorf("failed to record execution: %w", err)
	}

	return nil
}

// installWrappers installs monitors for enabled tools
func installWrappers(config *core.Config) error {
	for _, tool := range config.Monitoring.EnabledTools {
		monitor, err := newMonitor(core.NormalizeToolName(tool))
		if err != nil {
			continue
		}
		if err := monitor.Initialize(config); err != nil {
			fmt.Printf("Warning: failed to install %s wrapper: %v\n", tool, err)
		}
	}
	return nil
}

// installExecutableWrappers installs wrappers for discovered executables
func installExecutableWrappers(config *core.Config) error {
	targets := discoverExecutableWrappers(config)
	for _, target := range targets {
		if err := writeExecutableWrapper(config, target); err != nil {
			return err
		}
	}
	return nil
}

// discoverExecutableWrappers discovers executables to wrap
func discoverExecutableWrappers(config *core.Config) []executableWrapper {
	targets := make(map[string]executableWrapper)
	toolEnabled := func(tool string) bool {
		for _, enabled := range config.Monitoring.EnabledTools {
			if core.NormalizeToolName(enabled) == tool {
				return true
			}
		}
		return false
	}
	addExecutableDir := func(tool, dir string) {
		if dir == "" {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if shouldSkipExecutableWrapper(name) {
				continue
			}
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() || info.Mode()&core.ExecutableModeMask == 0 {
				continue
			}
			if _, exists := targets[name]; exists {
				continue
			}
			targets[name] = executableWrapper{
				Name:         name,
				OriginalPath: path,
				Tool:         tool,
				Package:      packageNameForExecutable(tool, path, name),
			}
		}
	}

	if toolEnabled(core.ToolHomebrew) {
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolHomebrew] {
			addExecutableDir(core.ToolHomebrew, dir)
		}
	}
	if toolEnabled(core.ToolNPM) {
		if npmBin := npmGlobalBinDir(); npmBin != "" {
			addExecutableDir(core.ToolNPM, npmBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolNPM] {
			addExecutableDir(core.ToolNPM, dir)
		}
	}
	if toolEnabled(core.ToolPNPM) {
		if pnpmBin := pnpmGlobalBinDir(); pnpmBin != "" {
			addExecutableDir(core.ToolPNPM, pnpmBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolPNPM] {
			addExecutableDir(core.ToolPNPM, dir)
		}
	}
	if toolEnabled(core.ToolBun) {
		if bunBin := bunGlobalBinDir(); bunBin != "" {
			addExecutableDir(core.ToolBun, bunBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolBun] {
			addExecutableDir(core.ToolBun, dir)
		}
	}
	if toolEnabled(core.ToolGo) {
		if goBin := goBinaryDir(config); goBin != "" {
			addExecutableDir(core.ToolGo, goBin)
		}
	}
	if toolEnabled(core.ToolPip) {
		if pythonBin := pythonUserBaseBinDir(); pythonBin != "" {
			addExecutableDir(core.ToolPip, pythonBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolPip] {
			addExecutableDir(core.ToolPip, dir)
		}
	}
	if toolEnabled(core.ToolUV) {
		if uvBin := uvToolBinDir(); uvBin != "" {
			addExecutableDir(core.ToolUV, uvBin)
		}
		for _, dir := range config.Monitoring.Filesystem.WatchPaths[core.ToolUV] {
			addExecutableDir(core.ToolUV, dir)
		}
	}

	results := make([]executableWrapper, 0, len(targets))
	for _, target := range targets {
		results = append(results, target)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results
}

// writeExecutableWrapper writes a wrapper script for an executable
func writeExecutableWrapper(config *core.Config, target executableWrapper) error {
	diuPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve diu executable: %w", err)
	}

	wrapperPath, err := executableWrapperPath(config.Monitoring.Process.WrapperDir, target.Name)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`#!/bin/bash
DIU_SOCKET="%s"
DIU_BINARY="%s"
ORIGINAL_BINARY="%s"
DIU_TOOL="%s"
DIU_PACKAGE="%s"
DIU_EXECUTABLE="%s"
START_TIME=$(date +%%s)

"$ORIGINAL_BINARY" "$@"
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
        "command": "$(json_escape "$DIU_EXECUTABLE $*")",
        "args": $args_json,
        "exit_code": $EXIT_CODE,
        "duration_ms": $DURATION,
        "timestamp": "$(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)",
        "working_dir": "$(json_escape "$(pwd)")",
        "user": "$(json_escape "$(whoami)")",
        "packages_affected": ["$(json_escape "$DIU_PACKAGE")"],
        "metadata": {
            "executable": "$(json_escape "$DIU_EXECUTABLE")",
            "original_path": "$(json_escape "$ORIGINAL_BINARY")"
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

    if [ "$sent" != true ] && [ -x "$DIU_BINARY" ]; then
        printf '%%s\n' "$payload" | "$DIU_BINARY" record >/dev/null 2>&1
    fi
} &>/dev/null &

exit $EXIT_CODE
`, core.ShellEscapeString(config.Daemon.SocketPath), core.ShellEscapeString(diuPath), core.ShellEscapeString(target.OriginalPath), core.ShellEscapeString(target.Tool), core.ShellEscapeString(target.Package), core.ShellEscapeString(target.Name))

	return writeOwnerExecutableFile(wrapperPath, []byte(script))
}
