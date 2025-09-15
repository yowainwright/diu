package wrappers

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/yowainwright/diu/internal/core"
)

type WrapperGenerator struct {
	config *core.Config
}

func NewWrapperGenerator(config *core.Config) *WrapperGenerator {
	return &WrapperGenerator{
		config: config,
	}
}

func (g *WrapperGenerator) GenerateWrapper(tool, originalPath string) error {
	wrapperDir := g.config.Monitoring.Process.WrapperDir
	if err := os.MkdirAll(wrapperDir, 0755); err != nil {
		return fmt.Errorf("failed to create wrapper directory: %w", err)
	}

	wrapperPath := filepath.Join(wrapperDir, tool)

	tmpl := template.Must(template.New("wrapper").Parse(wrapperTemplate))

	file, err := os.OpenFile(wrapperPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to create wrapper file: %w", err)
	}
	defer file.Close()

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
	for _, path := range paths {
		if path == g.config.Monitoring.Process.WrapperDir {
			continue
		}

		candidate := filepath.Join(path, binaryName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if info.Mode()&0111 != 0 {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("binary not found: %s", binaryName)
}

func (g *WrapperGenerator) updatePATH() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	shellConfigs := []string{
		filepath.Join(homeDir, ".bashrc"),
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".config", "fish", "config.fish"),
	}

	exportLine := fmt.Sprintf("export PATH=\"%s:$PATH\"", g.config.Monitoring.Process.WrapperDir)
	fishLine := fmt.Sprintf("set -gx PATH %s $PATH", g.config.Monitoring.Process.WrapperDir)

	for _, configFile := range shellConfigs {
		if _, err := os.Stat(configFile); err != nil {
			continue
		}

		content, err := os.ReadFile(configFile)
		if err != nil {
			continue
		}

		line := exportLine
		if filepath.Base(configFile) == "config.fish" {
			line = fishLine
		}

		if !contains(string(content), line) {
			file, err := os.OpenFile(configFile, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				continue
			}
			defer file.Close()

			file.WriteString("\n# DIU wrapper path\n")
			file.WriteString(line + "\n")
		}
	}

	return nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[len(s)-len(substr):] == substr
}