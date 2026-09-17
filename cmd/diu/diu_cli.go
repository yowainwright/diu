package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
)

type command = dx.Command
type flag = dx.Flag

func addReportFormatFlag(cmd *command) {
	var format string
	cmd.Flags().StringVarP(&format, "format", "f", formatTable, "Output format (table, json)")
}

func printJSON(value any) error {
	enc := json.NewEncoder(cliOutput().Stdout())
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func validateReportFormat(cmd *command) error {
	return validateOutputFormat(flagString(cmd, "format"), formatTable, formatJSON)
}

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
