package dx_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

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

func TestActivityReportsWarningAndFailure(t *testing.T) {
	t.Setenv("DIU_ACTIVITY", "never")
	t.Setenv("DIU_COLOR", "never")
	tests := []struct {
		name   string
		finish func(*dx.Activity)
		want   string
	}{
		{name: "warning", finish: func(activity *dx.Activity) { activity.Warning("check result") }, want: "[!] check result\n"},
		{name: "failure", finish: func(activity *dx.Activity) { activity.Fail("scan failed") }, want: "[x] scan failed\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			activity := dx.NewOut(&bytes.Buffer{}, &stderr, nil).StartActivity("loading")
			test.finish(activity)
			if stderr.String() != test.want {
				t.Fatalf("activity output = %q, want %q", stderr.String(), test.want)
			}
		})
	}
}

func TestActivityRestoresCursorWhenAnimated(t *testing.T) {
	output := animatedActivityOutput(t)
	assertCursorRestored(t, output)
	assertActivityCompletion(t, output)
}

func animatedActivityOutput(t *testing.T) string {
	t.Helper()

	t.Setenv("DIU_ACTIVITY", "always")
	t.Setenv("DIU_COLOR", "never")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	activity := out.StartActivity("loading")
	activity.Success("loaded")

	return stderr.String()
}

func assertCursorRestored(t *testing.T, output string) {
	t.Helper()

	hidesCursor := strings.Contains(output, "\x1b[?25l")
	showsCursor := strings.Contains(output, "\x1b[?25h")
	restoresCursor := hidesCursor && showsCursor
	if !restoresCursor {
		t.Fatalf("activity did not restore cursor: %q", output)
	}
}

func assertActivityCompletion(t *testing.T, output string) {
	t.Helper()

	if !strings.HasSuffix(output, "[ok] loaded\n") {
		t.Fatalf("activity completion = %q", output)
	}
}

func TestActivityRendersUpdatedMessage(t *testing.T) {
	t.Setenv("DIU_ACTIVITY", "always")
	t.Setenv("DIU_COLOR", "never")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	activity := out.StartActivity("starting")
	activity.Update("still working")
	time.Sleep(120 * time.Millisecond)
	activity.Stop()

	if !strings.Contains(stderr.String(), "still working") {
		t.Fatalf("activity output = %q", stderr.String())
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
	output := activityNoticeOutput(t)
	assertSingleCursorRestore(t, output)
	assertActivityStatusesOrdered(t, output)
}

func activityNoticeOutput(t *testing.T) string {
	t.Helper()

	t.Setenv("DIU_ACTIVITY", "always")
	t.Setenv("DIU_COLOR", "never")
	t.Setenv("TERM", "xterm-256color")
	var stderr bytes.Buffer
	out := dx.NewOut(&bytes.Buffer{}, &stderr, strings.NewReader(""))

	activity := out.StartActivity("loading")
	activity.Notice(dx.Warning, "partial result")
	activity.Success("loaded")

	return stderr.String()
}

func assertSingleCursorRestore(t *testing.T, output string) {
	t.Helper()

	if strings.Count(output, "\x1b[?25h") != 1 {
		t.Fatalf("cursor restore count in %q", output)
	}
}

func assertActivityStatusesOrdered(t *testing.T, output string) {
	t.Helper()

	warningIndex := strings.Index(output, "[!] partial result\n")
	successIndex := strings.Index(output, "[ok] loaded\n")
	hasWarning := warningIndex >= 0
	successAfterWarning := successIndex >= warningIndex
	statusesOrdered := hasWarning && successAfterWarning
	if !statusesOrdered {
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
