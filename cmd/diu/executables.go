package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yowainwright/diu/internal/core"
)

type executableTarget struct {
	Name         string
	OriginalPath string
	Tool         string
	Package      string
}

func currentShellHomeDirs() ([]string, error) {
	activeHome, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(activeHome) {
		return nil, fmt.Errorf("home directory must be absolute: %s", activeHome)
	}
	return []string{filepath.Clean(activeHome)}, nil
}

func discoverExecutables(config *core.Config) []executableTarget {
	targets := make(map[string]executableTarget)
	watchPaths := config.Monitoring.Filesystem.WatchPaths
	addToolExecutableDirs(config, targets, core.ToolHomebrew, watchPaths[core.ToolHomebrew])
	addToolExecutableDirs(config, targets, core.ToolNPM, executableDirs(npmGlobalBinDir(), watchPaths[core.ToolNPM]))
	addToolExecutableDirs(config, targets, core.ToolPNPM, executableDirs(pnpmGlobalBinDir(), watchPaths[core.ToolPNPM]))
	addToolExecutableDirs(config, targets, core.ToolBun, executableDirs(bunGlobalBinDir(), watchPaths[core.ToolBun]))
	addGoExecutableDirs(config, targets)
	addToolExecutableDirs(config, targets, core.ToolPip, executableDirs(pythonUserBaseBinDir(), watchPaths[core.ToolPip]))
	addToolExecutableDirs(config, targets, core.ToolUV, executableDirs(uvToolBinDir(), watchPaths[core.ToolUV]))
	return slices.SortedFunc(maps.Values(targets), compareExecutableTargets)
}

func compareExecutableTargets(a, b executableTarget) int {
	return strings.Compare(a.Name, b.Name)
}

func monitoringToolEnabled(config *core.Config, tool string) bool {
	for _, enabled := range config.Monitoring.EnabledTools {
		if core.NormalizeToolName(enabled) == tool {
			return true
		}
	}
	return false
}

func executableDirs(primary string, extra []string) []string {
	if primary == "" {
		return extra
	}
	return append([]string{primary}, extra...)
}

func addToolExecutableDirs(config *core.Config, targets map[string]executableTarget, tool string, dirs []string) {
	if !monitoringToolEnabled(config, tool) {
		return
	}
	for _, dir := range dirs {
		addExecutableDir(targets, tool, dir)
	}
}

func addGoExecutableDirs(config *core.Config, targets map[string]executableTarget) {
	if monitoringToolEnabled(config, core.ToolGo) {
		addExecutableDir(targets, core.ToolGoBinary, goBinaryDir(config))
	}
}

func addExecutableDir(targets map[string]executableTarget, tool, dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		addExecutableEntry(targets, tool, dir, entry.Name())
	}
}

func addExecutableEntry(targets map[string]executableTarget, tool, dir, name string) {
	if shouldSkipExecutable(name) {
		return
	}
	path := filepath.Join(dir, name)
	if !usableExecutablePath(path) {
		return
	}
	pkg := packageNameForExecutable(tool, path, name)
	if pkg == "" {
		return
	}
	if preferExistingExecutable(targets[name], path) {
		return
	}
	targets[name] = executableTarget{Name: name, OriginalPath: path, Tool: tool, Package: pkg}
}

func preferExistingExecutable(existing executableTarget, path string) bool {
	if existing.OriginalPath == "" {
		return false
	}
	return executablePathPriority(existing.OriginalPath) <= executablePathPriority(path)
}

func executablePathPriority(path string) int {
	paths := filepath.SplitList(os.Getenv("PATH"))
	for index, dir := range paths {
		if sameExecutable(filepath.Join(dir, filepath.Base(path)), path) {
			return index
		}
	}
	return len(paths)
}

func usableExecutablePath(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	return info.Mode()&core.ExecutableModeMask != 0
}
