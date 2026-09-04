package scripts_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHomebrewPublishUsesRetryableJob(t *testing.T) {
	path := filepath.Join(projectRoot(t), ".github", "workflows", "release.yml")
	workflow := readFile(t, path)
	homebrewStart := strings.Index(workflow, "\n  homebrew:\n")
	if homebrewStart < 0 {
		t.Fatal("release workflow has no separate Homebrew job")
	}

	releaseJob := workflow[:homebrewStart]
	homebrewJob := workflow[homebrewStart:]
	if strings.Contains(releaseJob, "- name: Publish Homebrew formula") {
		t.Fatal("release job still publishes the Homebrew formula")
	}
	if !strings.Contains(homebrewJob, "needs: release") {
		t.Fatal("Homebrew job does not wait for the release job")
	}
}

func TestWorkflowActionsUseFullCommitSHAs(t *testing.T) {
	paths := []string{
		filepath.Join(projectRoot(t), ".github", "workflows", "ci.yml"),
		filepath.Join(projectRoot(t), ".github", "workflows", "release.yml"),
	}
	for _, path := range paths {
		assertWorkflowActionPins(t, path)
	}
}

func assertWorkflowActionPins(t *testing.T, path string) {
	t.Helper()

	for _, line := range strings.Split(readFile(t, path), "\n") {
		ref, ok := workflowActionRef(line)
		hasMutableRef := ok && !isFullCommitSHA(ref)
		if hasMutableRef {
			t.Fatalf("%s uses mutable action ref: %s", path, strings.TrimSpace(line))
		}
	}
}

func workflowActionRef(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	uses := strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "uses: ")
	if uses == trimmed {
		return "", false
	}
	fields := strings.Fields(uses)
	if len(fields) == 0 {
		return "", false
	}
	value := fields[0]
	index := strings.LastIndex(value, "@")
	if index < 0 {
		return "", false
	}
	return value[index+1:], true
}

func isFullCommitSHA(ref string) bool {
	if len(ref) != 40 {
		return false
	}
	for _, char := range ref {
		if !isHexDigit(char) {
			return false
		}
	}
	return true
}

func isHexDigit(char rune) bool {
	isDigit := char >= '0' && char <= '9'
	isLowerHex := char >= 'a' && char <= 'f'
	return isDigit || isLowerHex
}

func TestVersionTaskRefreshesTagsBeforeSVU(t *testing.T) {
	path := filepath.Join(projectRoot(t), ".mise.toml")
	task := taskBlock(readFile(t, path), "[tasks.version]")
	fetchIndex := strings.Index(task, "git fetch --quiet origin")
	svuIndex := strings.Index(task, "svu next")
	hasOrderedRefresh := fetchIndex >= 0 && svuIndex > fetchIndex
	if !hasOrderedRefresh {
		t.Fatalf("version task must refresh tags before svu next:\n%s", task)
	}
}

func taskBlock(config, header string) string {
	start := strings.Index(config, header)
	if start < 0 {
		return ""
	}
	rest := config[start+len(header):]
	next := strings.Index(rest, "\n[")
	if next < 0 {
		return rest
	}
	return rest[:next]
}
