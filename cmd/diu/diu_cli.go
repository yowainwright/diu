package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
)

type command = dx.Command
type flag = dx.Flag

func validateOutputFormat(format string, allowed ...string) error {
	if slices.Contains(allowed, format) {
		return nil
	}
	return fmt.Errorf("invalid --format %q: expected %s", format, strings.Join(allowed, ", "))
}

func validateResultCount(name string, count int) error {
	if count < 0 {
		return fmt.Errorf("--%s must be non-negative", name)
	}
	return nil
}

func validateListFlags(cmd *command) error {
	format := flagString(cmd, "format")
	if err := validateOutputFormat(format, formatTable, formatJSON, formatCSV); err != nil {
		return err
	}
	return validateResultCount("limit", flagInt(cmd, "limit"))
}

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
