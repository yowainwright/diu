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

func TestOutRoutesFormattedTextToConfiguredStreams(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	out := dx.NewOut(&stdout, &stderr, strings.NewReader(""))

	out.Print("result ")
	out.Printf("%d", 2)
	out.UI("loading ")
	out.UIPrintf("%d", 3)
	out.UILine(" done")

	if stdout.String() != "result 2" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.String() != "loading 3 done\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestOutExposesConfiguredStreams(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	stdin := strings.NewReader("input")
	out := dx.NewOut(&stdout, &stderr, stdin)

	hasStdout := out.Stdout() == &stdout
	hasStderr := out.Stderr() == &stderr
	hasStdin := out.Stdin() == stdin
	hasConfiguredStreams := hasStdout && hasStderr && hasStdin
	if !hasConfiguredStreams {
		t.Fatal("Out did not retain its configured streams")
	}
}

func TestOutUsesSafeDefaultsForMissingStreams(t *testing.T) {
	t.Setenv("CI", "1")
	out := dx.NewOut(nil, nil, nil)

	out.Print("data")
	out.UI("status")
	hasStdout := out.Stdout() != nil
	hasStderr := out.Stderr() != nil
	hasStdin := out.Stdin() != nil
	hasStreams := hasStdout && hasStderr && hasStdin
	if !hasStreams {
		t.Fatal("missing stream was left nil")
	}
	if out.CanPrompt() {
		t.Fatal("prompting enabled without interactive streams")
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

func TestOutUsesSemanticToneColors(t *testing.T) {
	out := newColorTestOut(t)
	assertSemanticToneColors(t, out)
	assertPlainTone(t, out)
	assertUnknownTone(t, out)
}

func newColorTestOut(t *testing.T) *dx.Out {
	t.Helper()

	t.Setenv("DIU_COLOR", "always")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	return dx.NewOut(&bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader(""))
}

func assertSemanticToneColors(t *testing.T, out *dx.Out) {
	t.Helper()

	for _, test := range semanticToneColorTests() {
		assertSemanticToneColor(t, out, test)
	}
}

type semanticToneColorTest struct {
	tone dx.Tone
	code string
}

func semanticToneColorTests() []semanticToneColorTest {
	return []semanticToneColorTest{
		{tone: dx.Accent, code: "1;36"},
		{tone: dx.Success, code: "32"},
		{tone: dx.Warning, code: "33"},
		{tone: dx.Error, code: "31"},
		{tone: dx.Info, code: "36"},
		{tone: dx.Muted, code: "2"},
	}
}

func assertSemanticToneColor(t *testing.T, out *dx.Out, test semanticToneColorTest) {
	t.Helper()

	want := "\x1b[" + test.code + "mtext\x1b[0m"
	if got := out.StyleData(test.tone, "text"); got != want {
		t.Fatalf("DataStyle(%d) = %q, want %q", test.tone, got, want)
	}
}

func assertPlainTone(t *testing.T, out *dx.Out) {
	t.Helper()

	if got := out.StyleData(dx.Plain, "text"); got != "text" {
		t.Fatalf("plain text = %q", got)
	}
}

func assertUnknownTone(t *testing.T, out *dx.Out) {
	t.Helper()

	if got := out.StyleData(dx.Tone(255), "text"); got != "text" {
		t.Fatalf("unknown tone = %q", got)
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
