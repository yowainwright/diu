package monitors

import "github.com/yowainwright/diu/internal/core"

type CargoMonitor struct {
	*BaseMonitor
}

type cargoInventory struct {
	current  *core.PackageInfo
	binaries map[string]*core.PackageInfo
	hasBins  bool
}

type PipxMonitor struct {
	*BaseMonitor
}

type pipxSnapshot struct {
	Version string                     `json:"pipx_spec_version"`
	Venvs   map[string]pipxEnvironment `json:"venvs"`
}

type pipxEnvironment struct {
	Metadata pipxMetadata `json:"metadata"`
}

type pipxMetadata struct {
	MainPackage      *pipxPackage            `json:"main_package"`
	InjectedPackages map[string]*pipxPackage `json:"injected_packages"`
	ExposureEnabled  *bool                   `json:"exposure_enabled"`
}

type pipxPackage struct {
	Package                   string                `json:"package"`
	Version                   string                `json:"package_version"`
	Suffix                    string                `json:"suffix"`
	Apps                      []string              `json:"apps"`
	ShouldIncludeApps         bool                  `json:"include_apps"`
	AppPaths                  []pipxPath            `json:"app_paths"`
	ShouldIncludeDependencies bool                  `json:"include_dependencies"`
	DependencyPaths           map[string][]pipxPath `json:"app_paths_of_dependencies"`
	IncludeResources          []string              `json:"include_resources_from"`
}

type pipxPath struct {
	Type string `json:"__type__"`
	Path string `json:"__Path__"`
}

type RubyGemsMonitor struct {
	*BaseMonitor
}

type rubyGemsSnapshot struct {
	Schema   int              `json:"schema"`
	Ruby     string           `json:"ruby"`
	Packages []rubyGemPackage `json:"packages"`
}

type rubyGemPackage struct {
	Name        string              `json:"name"`
	Version     string              `json:"version"`
	Root        string              `json:"root"`
	Path        string              `json:"path"`
	Platform    string              `json:"platform"`
	IsDefault   *bool               `json:"default"`
	Executables []rubyGemExecutable `json:"executables"`
}

type rubyGemExecutable struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
