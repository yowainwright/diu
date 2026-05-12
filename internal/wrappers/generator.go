package wrappers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

type WrapperGenerator struct {
	config  *core.Config
	homeDir string
}

func NewWrapperGenerator(config *core.Config) *WrapperGenerator {
	homeDir, _ := os.UserHomeDir()
	return &WrapperGenerator{
		config:  config,
		homeDir: homeDir,
	}
}

func (g *WrapperGenerator) GenerateWrapper(tool, originalPath string) (err error) {
	wrapperDir := g.config.Monitoring.Process.WrapperDir
	if err := os.MkdirAll(wrapperDir, core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create wrapper directory: %w", err)
	}

	wrapperPath, err := executableWrapperPath(wrapperDir, tool)
	if err != nil {
		return err
	}

	tmpl := template.Must(template.New("wrapper").Parse(wrapperTemplate))

	file, err := safefs.OpenFile(wrapperPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to create wrapper file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close wrapper file: %w", closeErr)
		}
	}()

	data := struct {
		Tool         string
		OriginalPath string
		APIEndpoint  string
	}{
		Tool:         tool,
		OriginalPath: originalPath,
		APIEndpoint:  fmt.Sprintf("http://%s:%d/api/v1/executions", g.config.API.Host, g.config.API.Port),
	}

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to write wrapper: %w", err)
	}

	if err := file.Chmod(core.OwnerExecutableMode); err != nil {
		return fmt.Errorf("failed to set wrapper permissions: %w", err)
	}

	return nil
}

func (g *WrapperGenerator) InstallWrappers() error {
	tools := g.config.Monitoring.EnabledTools

	for _, tool := range tools {
		originalPath, err := g.findOriginalBinary(tool)
		if err != nil {
			fmt.Printf("Warning: %s not found, skipping wrapper\n", tool)
			continue
		}

		if err := g.GenerateWrapper(tool, originalPath); err != nil {
			return fmt.Errorf("failed to generate wrapper for %s: %w", tool, err)
		}
	}

	return g.updatePATH()
}

func (g *WrapperGenerator) findOriginalBinary(tool string) (string, error) {
	// Map tool names to binary names
	binaryName := tool
	switch tool {
	case "homebrew":
		binaryName = "brew"
	case "python":
		binaryName = "pip"
	}

	paths := filepath.SplitList(os.Getenv("PATH"))
	wrapperDir := filepath.Clean(g.config.Monitoring.Process.WrapperDir)
	for _, path := range paths {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) == wrapperDir {
			continue
		}

		candidate := filepath.Join(path, binaryName)
		if info, err := safefs.Stat(candidate); err == nil && !info.IsDir() {
			if info.Mode()&core.ExecutableModeMask != 0 {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("binary not found: %s", binaryName)
}

func (g *WrapperGenerator) updatePATH() error {
	shellConfigs := []string{
		filepath.Join(g.homeDir, ".bashrc"),
		filepath.Join(g.homeDir, ".zshrc"),
		filepath.Join(g.homeDir, ".config", "fish", "config.fish"),
	}

	exportLine := fmt.Sprintf("export PATH=\"%s:$PATH\"", g.config.Monitoring.Process.WrapperDir)
	fishLine := fmt.Sprintf("set -gx PATH %s $PATH", g.config.Monitoring.Process.WrapperDir)

	for _, configFile := range shellConfigs {
		if _, err := safefs.Stat(configFile); err != nil {
			continue
		}

		content, err := safefs.ReadFile(configFile)
		if err != nil {
			continue
		}

		line := exportLine
		if filepath.Base(configFile) == "config.fish" {
			line = fishLine
		}

		if !contains(string(content), line) {
			if err := appendShellConfigLines(configFile, "\n# DIU wrapper path\n", line+"\n"); err != nil {
				continue
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

func executableWrapperPath(wrapperDir, name string) (string, error) {
	if strings.TrimSpace(wrapperDir) == "" {
		return "", fmt.Errorf("wrapper directory cannot be empty")
	}
	if name == "" || strings.HasPrefix(name, ".") || filepath.Base(name) != name {
		return "", fmt.Errorf("invalid wrapper name: %s", name)
	}

	cleanDir := filepath.Clean(wrapperDir)
	wrapperPath := filepath.Join(cleanDir, name)
	relativePath, err := filepath.Rel(cleanDir, wrapperPath)
	if err != nil {
		return "", fmt.Errorf("failed to validate wrapper path: %w", err)
	}
	if relativePath == "." || strings.HasPrefix(relativePath, "..") {
		return "", fmt.Errorf("wrapper path escapes wrapper directory: %s", wrapperPath)
	}

	return wrapperPath, nil
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
