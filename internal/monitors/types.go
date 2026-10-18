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
