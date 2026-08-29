package main

import (
	"fmt"
	"io"
	"os"

	"github.com/yowainwright/diu/internal/dx"
)

type styleguideToneSample struct {
	name string
	tone dx.Tone
	text string
}

func runRootCommand(cmd *command, args []string) error {
	if flagBool(cmd, "styleguide") {
		renderStyleguide(commandStyleguideOutput(cmd))
		return nil
	}
	if len(args) > 0 {
		cmd.PrintErrorUsage()
		return fmt.Errorf("unknown command: %s", args[0])
	}
	cmd.PrintUsage()
	return nil
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
