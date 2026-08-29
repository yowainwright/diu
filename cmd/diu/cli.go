package main

import (
	"fmt"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
)

type command = dx.Command
type flag = dx.Flag

func coreVersion() string {
	if version == "" {
		return core.CurrentVersion()
	}
	if version == "dev" {
		return core.CurrentVersion()
	}
	return version
}

func versionString() string {
	return fmt.Sprintf("diu %s (commit %s, built %s)", coreVersion(), commit, date)
}
