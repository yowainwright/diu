package main

import (
	"fmt"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
)

type command = dx.Command
type flag = dx.Flag

func coreVersion() string {
	if isDefaultVersion(version) {
		return core.CurrentVersion()
	}
	return version
}

func isDefaultVersion(value string) bool {
	if value == "" {
		return true
	}
	return value == "dev"
}

func versionString() string {
	return fmt.Sprintf("diu %s (commit %s, built %s)", coreVersion(), commit, date)
}
