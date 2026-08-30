package dx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

var ErrDeadlineRequired = errors.New("command context requires a deadline")

type Runner struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

type CommandError struct {
	Name string
	Code int
	Err  error
}

func NewRunner(stdin io.Reader, stdout, stderr io.Writer) *Runner {
	return &Runner{
		stdin:  readerOrEmpty(stdin),
		stdout: writerOrDiscard(stdout),
		stderr: writerOrDiscard(stderr),
	}
}

func TerminalRunner() *Runner {
	return NewRunner(os.Stdin, os.Stdout, os.Stderr)
}

func (r *Runner) Run(ctx context.Context, name string, args ...string) error {
	if _, ok := ctx.Deadline(); !ok {
		return ErrDeadlineRequired
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("command name cannot be empty")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.run(ctx, name, args)
}

func (r *Runner) run(ctx context.Context, name string, args []string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = r.stdin
	command.Stdout = r.stdout
	command.Stderr = r.stderr
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s failed: %w", name, ctxErr)
	}
	return commandResult(name, err)
}

func commandResult(name string, err error) error {
	if err == nil {
		return nil
	}
	return &CommandError{Name: name, Code: commandExitCode(err), Err: err}
}

func commandExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("%s failed with exit code %d: %v", e.Name, e.Code, e.Err)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func ExitCode(err error) (int, bool) {
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		return 0, false
	}
	return commandErr.Code, true
}
