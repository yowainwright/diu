package dx_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/dx"
)

func TestRunnerRequiresDeadline(t *testing.T) {
	runner := dx.NewRunner(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	err := runner.Run(context.Background(), "true")
	if !errors.Is(err, dx.ErrDeadlineRequired) {
		t.Fatalf("Run error = %v, want ErrDeadlineRequired", err)
	}
}

func TestRunnerExecutesArgvWithoutShell(t *testing.T) {
	var stdout bytes.Buffer
	runner := dx.NewRunner(strings.NewReader(""), &stdout, &bytes.Buffer{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := runner.Run(ctx, "printf", "%s", "; echo injected"); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if stdout.String() != "; echo injected" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunnerPreservesExitCode(t *testing.T) {
	runner := dx.NewRunner(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := runner.Run(ctx, "false")
	code, ok := dx.ExitCode(err)
	if !ok || code != 1 {
		t.Fatalf("ExitCode = %d, %v for %v", code, ok, err)
	}
}
