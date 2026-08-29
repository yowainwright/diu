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
