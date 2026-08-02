package main

import (
	"fmt"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
)

type command = dx.Command
type flag = dx.Flag

func coreVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	return core.Version
}

func versionString() string {
	return fmt.Sprintf("diu %s (commit %s, built %s)", coreVersion(), commit, date)
}
