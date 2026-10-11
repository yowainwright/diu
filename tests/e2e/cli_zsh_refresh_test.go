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

	if err := os.Remove(filepath.Join(f.managed, "probe")); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, filepath.Join(f.managed, "listener-tool"), "#!/bin/sh\nexit 17\n", 0o700)
	assertCLIExit(t, f.cli(t, "scan"), 0)
	writeCLIFile(t, filepath.Join(f.managed, "sudo"), "#!/bin/sh\ncase \"$1\" in -u|-g) shift 2 ;; esac\nexec \"$@\"\n", 0o700)

	untrackedCommands := strings.Join([]string{
		"echo listener-tool",
		"echo if listener-tool",
		"echo else listener-tool",
		"cat < /dev/null > listener-tool",
		"builtin listener-tool",
		"builtin -- listener-tool",
		"command -v listener-tool",
		"command -V listener-tool",
		"command -pv listener-tool",
		"sudo -l listener-tool || true",
		"case x in x) true ;; listener-tool) true ;; esac",
		"probe || true",
	}, "\n") + "\nprint -r -- DIU_'UNTRACKED_DONE'\n"
	writeZshInput(t, input, untrackedCommands)
	waitCLIZshOutput(t, output, "DIU_UNTRACKED_DONE")
	records := waitCLIRecords(t, f, 0)
	if len(records) != 0 {
		t.Fatalf("untracked commands produced launch records: %#v", records)
	}

	trackedCommands := []string{
		"listener-tool || true",
		"true | listener-tool || true",
		"if listener-tool; then true; fi",
		"(exec -a custom-name listener-tool || true)",
		"{ listener-tool; }",
		"echo exec -a; listener-tool || true",
		"echo case; listener-tool || true",
		"echo for; listener-tool || true",
		"command -p " + filepath.Join(f.managed, "listener-tool") + " || true",
		"env -i PATH=" + f.basePath() + " DIU_TEST_PREFIX=1 listener-tool || true",
		"sudo -u nobody listener-tool || true",
		"sudo -g staff listener-tool || true",
		filepath.Join(f.managed, "listener-tool") + " || true",
	}
	for index, trackedCommand := range trackedCommands {
		writeZshInput(t, input, trackedCommand+"\nprint -r -- DIU_'TRACKED_DONE'\n")
		waitCLIZshOutput(t, output, "DIU_TRACKED_DONE")
		records = waitCLIRecords(t, f, index+1)
		if len(records) != index+1 {
			t.Fatalf("record count = %d after launch %d, want %d: %#v", len(records), index+1, index+1, records)
		}
	}
	writeZshInput(t, input, "exit\n")
	_ = input.Close()
	if err := command.Wait(); err != nil {
		t.Fatalf("interactive zsh failed: %v; stderr=%s", err, stderr.String())
	}

	time.Sleep(100 * time.Millisecond)
	records = waitCLIRecords(t, f, len(trackedCommands))
	if len(records) != len(trackedCommands) {
		t.Fatalf("record count = %d, want %d tracked launches and no argument matches: %#v", len(records), len(trackedCommands), records)
	}
	for _, record := range records {
		assertCLIZshRecordedLaunchAttempt(t, record)
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
	writeZshInput(t, input, "print -r -- DIU_READY_$((1+1))\n")
	waitCLIZshOutput(t, output, "DIU_READY_2")
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
