package dx_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestRunnerRejectsEmptyCommandName(t *testing.T) {
	runner := dx.NewRunner(nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := runner.Run(ctx, "   ")
	if err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestRunnerReturnsPreexistingContextError(t *testing.T) {
	runner := dx.NewRunner(nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()

	err := runner.Run(ctx, "true")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestRunnerStopsCommandAtDeadline(t *testing.T) {
	runner := dx.NewRunner(nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := runner.Run(ctx, "sleep", "1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want deadline exceeded", err)
	}
	if _, ok := dx.ExitCode(err); ok {
		t.Fatalf("deadline error reported as command exit: %v", err)
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

func TestRunnerReportsCommandStartFailure(t *testing.T) {
	runner := dx.NewRunner(nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	name := filepath.Join(t.TempDir(), "missing")

	err := runner.Run(ctx, name)
	var commandErr *dx.CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("Run error = %T, want CommandError", err)
	}
	if commandErr.Code != -1 || !strings.Contains(commandErr.Error(), name) {
		t.Fatalf("CommandError = %#v", commandErr)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Run error does not expose os.ErrNotExist: %v", err)
	}
	if code, ok := dx.ExitCode(err); !ok || code != -1 {
		t.Fatalf("ExitCode = %d, %v", code, ok)
	}
}
