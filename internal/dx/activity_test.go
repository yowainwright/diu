package dx_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/dx"
)

func TestActivityUsesStaticCompletionOutsideTerminal(t *testing.T) {
	t.Setenv("DIU_ACTIVITY", "never")
	t.Setenv("DIU_COLOR", "never")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	out := dx.NewOut(&stdout, &stderr, strings.NewReader(""))

	activity := out.StartActivity("loading")
	activity.Success("loaded")

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if stderr.String() != "[ok] loaded\n" {
		t.Fatalf("stderr = %q, want static completion", stderr.String())
	}
}

func TestActivityRestoresCursorWhenAnimated(t *testing.T) {
	t.Setenv("DIU_ACTIVITY", "always")
	t.Setenv("DIU_COLOR", "never")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	activity := out.StartActivity("loading")
	activity.Success("loaded")

	output := stderr.String()
	if !strings.Contains(output, "\x1b[?25l") || !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("activity did not restore cursor: %q", output)
	}
	if !strings.HasSuffix(output, "[ok] loaded\n") {
		t.Fatalf("activity completion = %q", output)
	}
}

func TestActivityStopIsIdempotent(t *testing.T) {
	t.Setenv("DIU_ACTIVITY", "always")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	activity := out.StartActivity("loading")
	activity.Stop()
	activity.Stop()

	if strings.Count(stderr.String(), "\x1b[?25h") != 1 {
		t.Fatalf("cursor restore count in %q", stderr.String())
	}
}

func TestActivityNoticeStopsAnimationBeforeStatus(t *testing.T) {
	t.Setenv("DIU_ACTIVITY", "always")
	t.Setenv("DIU_COLOR", "never")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	activity := out.StartActivity("loading")
	activity.Notice(dx.Warning, "partial result")
	activity.Success("loaded")

	output := stderr.String()
	if strings.Count(output, "\x1b[?25h") != 1 {
		t.Fatalf("cursor restore count in %q", output)
	}
	warningIndex := strings.Index(output, "[!] partial result\n")
	successIndex := strings.Index(output, "[ok] loaded\n")
	if warningIndex < 0 || successIndex < warningIndex {
		t.Fatalf("activity statuses interleaved: %q", output)
	}
}

func TestShineAndGlitterStayPlainWithoutColor(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	t.Setenv("DIU_ACTIVITY", "never")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	if got := out.Shine("diu"); got != "diu" {
		t.Fatalf("Shine = %q, want plain text", got)
	}
	out.Glitter("ready")
	if stderr.String() != "[>] ready\n" {
		t.Fatalf("Glitter = %q, want restrained status", stderr.String())
	}
}
