//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCLIZshListenerRefreshesOpenShellAndMatchesCommands(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, "print -r -- DIU_READY\n")
	waitCLIZshOutput(t, output, "DIU_READY")

	if err := os.Remove(filepath.Join(f.managed, "probe")); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, filepath.Join(f.managed, "listener-tool"), "#!/bin/sh\nexit 17\n", 0o700)
	assertCLIExit(t, f.cli(t, "scan"), 0)

	commands := strings.Join([]string{
		"echo listener-tool",
		"cat < /dev/null > listener-tool",
		"probe || true",
		"true | listener-tool || true",
		"if listener-tool; then true; fi",
		"print -r -- DIU_DONE",
	}, "\n") + "\n"
	writeZshInput(t, input, commands)
	waitCLIZshOutput(t, output, "DIU_DONE")
	writeZshInput(t, input, "exit\n")
	_ = input.Close()
	if err := command.Wait(); err != nil {
		t.Fatalf("interactive zsh failed: %v; stderr=%s", err, stderr.String())
	}

	records := waitCLIRecords(t, f, 2)
	if len(records) != 2 {
		t.Fatalf("record count = %d, want only the pipeline and if commands: %#v", len(records), records)
	}
	for _, record := range records {
		assertCLIRecordedLaunchIdentity(t, record, "homebrew", "listener-tool")
	}
}

func startCLIZsh(t *testing.T, f *cliFixture) (*exec.Cmd, io.WriteCloser, <-chan string, *bytes.Buffer) {
	t.Helper()
	command := f.command(t, "script", "-q", "-e", "-c", "zsh -i", "/dev/null")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	outputPipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
	})
	output := make(chan string, 128)
	go func() {
		scanner := bufio.NewScanner(outputPipe)
		for scanner.Scan() {
			output <- scanner.Text()
		}
		close(output)
	}()
	return command, input, output, stderr
}

func writeZshInput(t *testing.T, input io.Writer, contents string) {
	t.Helper()
	if _, err := io.WriteString(input, contents); err != nil {
		t.Fatal(err)
	}
}

func waitCLIZshOutput(t *testing.T, output <-chan string, expected string) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	var seen []string
	for {
		select {
		case line, ok := <-output:
			if !ok {
				t.Fatalf("zsh output closed before %q; output=%q", expected, strings.Join(seen, "\n"))
			}
			seen = append(seen, line)
			if strings.Contains(line, expected) {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %q in zsh output: %q", expected, strings.Join(seen, "\n"))
		}
	}
}
