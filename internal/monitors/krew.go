package monitors

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yowainwright/diu/internal/core"
)

func NewKrewMonitor() Monitor {
	return &KrewMonitor{BaseMonitor: NewBaseMonitor(core.ToolKrew)}
}

func (m *KrewMonitor) Initialize(config *core.Config) error {
	if _, err := exec.LookPath("kubectl-krew"); err != nil {
		return fmt.Errorf("krew not found: %w", err)
	}
	return m.BaseMonitor.Initialize(config)
}

func (m *KrewMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return newExecutionRecord(core.ToolKrew, cmd, args), nil
}

//nolint:legibility // Monitor interface requires this method name.
func (m *KrewMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	root, err := krewRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "receipts"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Krew receipts: %w", err)
	}
	return krewPackages(root, entries)
}

func KrewExecutables() (map[string]*core.PackageInfo, error) {
	packages, err := NewKrewMonitor().GetInstalledPackages()
	if err != nil {
		return nil, err
	}
	root, err := krewRoot()
	if err != nil {
		return nil, err
	}
	return krewBinaryMap(root, packages), nil
}

func krewBinaryMap(root string, packages []*core.PackageInfo) map[string]*core.PackageInfo {
	binaries := make(map[string]*core.PackageInfo)
	for _, pkg := range packages {
		if pkg.Path == "" {
			continue
		}
		binary := *pkg
		name := krewExecutableName(pkg.Name)
		binary.Path = filepath.Join(root, "bin", name)
		binaries[name] = &binary
	}
	return binaries
}

func krewRoot() (string, error) {
	root := os.Getenv("KREW_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find Krew home: %w", err)
		}
		root = filepath.Join(home, ".krew")
	}
	return filepath.Abs(root)
}

func krewPackages(root string, entries []os.DirEntry) ([]*core.PackageInfo, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Krew root: %w", err)
	}
	var packages []*core.PackageInfo
	pluginName := regexp.MustCompile(`^[\w-]+$`)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".yaml")
		valid := entry.Type().IsRegular() && pluginName.MatchString(name)
		if !valid {
			return nil, fmt.Errorf("invalid Krew receipt: %s", entry.Name())
		}
		packages = append(packages, krewPackage(resolved, name))
	}
	return packages, nil
}

func krewPackage(root, name string) *core.PackageInfo {
	pkg := &core.PackageInfo{Name: name, Tool: core.ToolKrew}
	link := filepath.Join(root, "bin", krewExecutableName(name))
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return pkg
	}
	store := filepath.Join(root, "store", name)
	relative, err := filepath.Rel(store, target)
	if err != nil {
		return pkg
	}
	parts := strings.Split(relative, string(filepath.Separator))
	valid := filepath.IsLocal(relative) && len(parts) >= 2 && krewExecutableFile(target)
	if valid {
		pkg.Version = parts[0]
		pkg.Path = filepath.Join(store, pkg.Version)
	}
	return pkg
}

func krewExecutableName(name string) string {
	return "kubectl-" + strings.ReplaceAll(name, "-", "_")
}

func krewExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	isExecutable := info.Mode()&core.ExecutableModeMask != 0
	return info.Mode().IsRegular() && isExecutable
}
