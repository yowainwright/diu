package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/storage"
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

const (
	formatTable    = "table"
	formatJSON     = "json"
	formatCSV      = "csv"
	commandTimeout = 2 * time.Minute
)

func cliOutput() *dx.Out {
	return dx.TerminalOut()
}

func openStore() (*storage.JSONStorage, error) {
	config, err := core.LoadConfig("")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return nil, fmt.Errorf("failed to open storage: %w", err)
	}
	return store, nil
}

func closeStore(store storage.Storage) {
	if err := store.Close(); err != nil {
		cliOutput().Status(dx.Error, fmt.Sprintf("failed to close storage: %v", err))
	}
}

func closeStoreDuringActivity(store storage.Storage, activity *dx.Activity) {
	if err := store.Close(); err != nil {
		activity.Notice(dx.Error, fmt.Sprintf("failed to close storage: %v", err))
	}
}

func isTerminal() bool {
	return cliOutput().CanPrompt()
}

func flagString(cmd *command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}

func flagInt(cmd *command, name string) int {
	value, _ := cmd.Flags().GetInt(name)
	return value
}

func flagBool(cmd *command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)
	return value
}

func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		return parseDurationUnit(s, "d", 24*time.Hour)
	}

	if strings.HasSuffix(s, "w") {
		week := 7 * 24 * time.Hour
		return parseDurationUnit(s, "w", week)
	}

	if strings.HasSuffix(s, "mo") {
		month := 30 * 24 * time.Hour
		return parseDurationUnit(s, "mo", month)
	}

	return time.ParseDuration(s)
}

func parseDurationUnit(s, suffix string, unit time.Duration) (time.Duration, error) {
	count, err := strconv.Atoi(strings.TrimSuffix(s, suffix))
	if err != nil {
		return 0, err
	}
	duration := time.Duration(count) * unit
	return duration, nil
}

type styleguideToneSample struct {
	name string
	tone dx.Tone
	text string
}

func commandStyleguideOutput(cmd *command) *dx.Out {
	stdout := writerWithFallback(cmd.Output, os.Stdout)
	stderr := writerWithFallback(cmd.ErrorOutput, os.Stderr)
	return dx.NewOut(stdout, stderr, os.Stdin)
}

func writerWithFallback(writer io.Writer, fallback io.Writer) io.Writer {
	if writer != nil {
		return writer
	}
	return fallback
}

func renderStyleguide(out *dx.Out) {
	out.Println(out.StyleData(dx.Accent, "DIU Styleguide"))
	printStyleguideTones(out)
	printStyleguideStatuses(out)
	printStyleguideTable(out)
	printStyleguideProgress(out)
}

func printStyleguideTones(out *dx.Out) {
	out.Println()
	out.Println(out.StyleData(dx.Muted, "Tones"))
	for _, sample := range styleguideToneSamples() {
		out.Printf("  %-8s %s\n", sample.name, out.StyleData(sample.tone, sample.text))
	}
}

func styleguideToneSamples() []styleguideToneSample {
	return []styleguideToneSample{
		{name: "Accent", tone: dx.Accent, text: "section title"},
		{name: "Success", tone: dx.Success, text: "ready"},
		{name: "Warning", tone: dx.Warning, text: "attention"},
		{name: "Error", tone: dx.Error, text: "failed"},
		{name: "Info", tone: dx.Info, text: "detail"},
		{name: "Muted", tone: dx.Muted, text: "metadata"},
	}
}

func printStyleguideStatuses(out *dx.Out) {
	out.Println()
	out.Println(out.StyleData(dx.Muted, "Status"))
	out.Printf("  %s setup complete\n", out.StyleData(dx.Success, "[ok]"))
	out.Printf("  %s using fallback\n", out.StyleData(dx.Warning, "[!]"))
	out.Printf("  %s failed check\n", out.StyleData(dx.Error, "[x]"))
	out.Printf("  %s scanned packages\n", out.StyleData(dx.Info, "[i]"))
}

func printStyleguideTable(out *dx.Out) {
	out.Println()
	out.Println(out.StyleData(dx.Muted, "Table"))
	headers := []string{out.StyleData(dx.Accent, "FIELD"), out.StyleData(dx.Accent, "VALUE")}
	rows := styleguideTableRows(out)
	out.Println(dx.Table(headers, rows))
}

func styleguideTableRows(out *dx.Out) [][]string {
	return [][]string{
		{out.StyleData(dx.Muted, "Storage"), out.StyleData(dx.Success, "ready")},
		{out.StyleData(dx.Muted, "Daemon"), out.StyleData(dx.Warning, "not running")},
		{out.StyleData(dx.Muted, "Latest"), out.StyleData(dx.Info, "npm install eslint")},
	}
}

func printStyleguideProgress(out *dx.Out) {
	out.Println()
	out.Println(out.StyleData(dx.Muted, "Progress"))
	out.Println("  " + out.StyleData(dx.Info, dx.Progress(6, 10, 20)))
}
