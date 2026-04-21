package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const startMarker = "# diu shell hooks"
const endMarker = "# end diu shell hooks"

type Manager struct {
	Name string
}

var DefaultManagers = []Manager{
	{"brew"},
	{"npm"},
	{"go"},
	{"pip"},
	{"pip3"},
	{"cargo"},
	{"gem"},
}

// Setup injects shell hooks into detected RC files. Returns the list of files modified.
func Setup() ([]string, error) {
	rcFiles := detectRCFiles()
	if len(rcFiles) == 0 {
		return nil, fmt.Errorf("no shell config files found (~/.zshrc or ~/.bashrc)")
	}

	hooks := buildHooks()
	var installed []string

	for _, rcFile := range rcFiles {
		already, err := injectHooks(rcFile, hooks)
		if err != nil {
			return installed, fmt.Errorf("failed to modify %s: %w", rcFile, err)
		}
		if !already {
			installed = append(installed, rcFile)
		}
	}
	return installed, nil
}

// Teardown removes shell hooks from detected RC files. Returns the list of files modified.
func Teardown() ([]string, error) {
	rcFiles := detectRCFiles()
	var removed []string

	for _, rcFile := range rcFiles {
		was, err := removeHooks(rcFile)
		if err != nil {
			return removed, fmt.Errorf("failed to modify %s: %w", rcFile, err)
		}
		if was {
			removed = append(removed, rcFile)
		}
	}
	return removed, nil
}

// Status returns a map of RC file path → whether hooks are installed.
func Status() map[string]bool {
	result := make(map[string]bool)
	for _, f := range detectRCFiles() {
		result[f] = hasHooks(f)
	}
	return result
}

func detectRCFiles() []string {
	homeDir, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".bashrc"),
	}
	var found []string
	for _, f := range candidates {
		if _, err := os.Stat(f); err == nil {
			found = append(found, f)
		}
	}
	return found
}

func buildHooks() string {
	var b strings.Builder
	b.WriteString(startMarker + "\n")
	for _, m := range DefaultManagers {
		fmt.Fprintf(&b, "function %s() {\n", m.Name)
		fmt.Fprintf(&b, "  command %s \"$@\"\n", m.Name)
		b.WriteString("  local _diu_exit=$?\n")
		fmt.Fprintf(&b, "  command -v diu &>/dev/null && diu record --tool %s --exit-code $_diu_exit -- \"$@\" 2>/dev/null &\n", m.Name)
		b.WriteString("  return $_diu_exit\n")
		b.WriteString("}\n")
	}
	b.WriteString(endMarker + "\n")
	return b.String()
}

func injectHooks(rcFile, hooks string) (alreadyPresent bool, err error) {
	data, err := os.ReadFile(rcFile)
	if err != nil {
		return false, err
	}
	if strings.Contains(string(data), startMarker) {
		return true, nil
	}
	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n%s\n", hooks)
	return false, err
}

func removeHooks(rcFile string) (wasPresent bool, err error) {
	data, err := os.ReadFile(rcFile)
	if err != nil {
		return false, err
	}
	content := string(data)
	if !strings.Contains(content, startMarker) {
		return false, nil
	}

	// Prefer removing the preceding newline too for a clean edit.
	start := strings.Index(content, "\n"+startMarker)
	if start == -1 {
		start = strings.Index(content, startMarker)
	} else {
		start++ // include the newline before the marker
	}

	end := strings.Index(content, endMarker)
	if end == -1 {
		return true, fmt.Errorf("start marker found but end marker missing in %s", rcFile)
	}
	end += len(endMarker)
	if end < len(content) && content[end] == '\n' {
		end++
	}

	cleaned := content[:start] + content[end:]
	return true, atomicWrite(rcFile, []byte(cleaned))
}

func hasHooks(rcFile string) bool {
	data, err := os.ReadFile(rcFile)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), startMarker)
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".diu.tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
