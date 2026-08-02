package dx_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/dx"
)

func TestOutSeparatesDataFromStatus(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	t.Setenv("DIU_ACTIVITY", "never")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	out := dx.NewOut(&stdout, &stderr, strings.NewReader(""))

	out.Println("result")
	out.Status(dx.Success, "complete")

	if stdout.String() != "result\n" {
		t.Fatalf("stdout = %q, want result", stdout.String())
	}
	if stderr.String() != "[ok] complete\n" {
		t.Fatalf("stderr = %q, want completion status", stderr.String())
	}
}

func TestNullDeviceIsNotInteractive(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("Open null device: %v", err)
	}
	defer func() {
		if err := null.Close(); err != nil {
			t.Fatalf("Close null device: %v", err)
		}
	}()

	if dx.IsTerminal(null) {
		t.Fatal("null device reported as an interactive terminal")
	}
}

func TestTermDumbDisablesForcedColor(t *testing.T) {
	t.Setenv("DIU_COLOR", "always")
	t.Setenv("TERM", "dumb")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	out.Status(dx.Error, "failed")
	if strings.Contains(stderr.String(), "\x1b[") {
		t.Fatalf("status = %q, want no ANSI", stderr.String())
	}
}

func TestStatusDoesNotClearWithoutActiveLoader(t *testing.T) {
	t.Setenv("DIU_ACTIVITY", "always")
	t.Setenv("DIU_COLOR", "never")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	out.Status(dx.Info, "ready")
	if stderr.String() != "[i] ready\n" {
		t.Fatalf("status = %q, want no cursor control", stderr.String())
	}
}

func TestOutCanForceStandardAnsiColor(t *testing.T) {
	t.Setenv("DIU_COLOR", "always")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	out.Status(dx.Error, "failed")

	if !strings.Contains(stderr.String(), "\x1b[31m[x]\x1b[0m failed") {
		t.Fatalf("status = %q, want standard red ANSI", stderr.String())
	}
}

func TestOutHonorsNoColor(t *testing.T) {
	t.Setenv("DIU_COLOR", "always")
	t.Setenv("NO_COLOR", "1")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	out.Status(dx.Success, "complete")

	if strings.Contains(stderr.String(), "\x1b[") {
		t.Fatalf("status = %q, want no ANSI", stderr.String())
	}
}
