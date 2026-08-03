package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTagScriptPublishesSVUNextVersion(t *testing.T) {
	foreignIndex := newForeignGitIndex(t)
	t.Setenv("GIT_INDEX_FILE", foreignIndex)
	repo, origin := newReleaseRepo(t)
	logPath := filepath.Join(t.TempDir(), "release.log")

	output, err := runTagScript(t, repo, origin, logPath, "y\n")
	if err != nil {
		t.Fatalf("tag script failed: %v\n%s", err, output)
	}

	if !remoteTagExists(origin, "v0.2.0") {
		t.Fatal("expected v0.2.0 to be pushed")
	}
	if got := readFile(t, logPath); got != "run release-preview\n" {
		t.Fatalf("release preview calls = %q", got)
	}
}

func newForeignGitIndex(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	repo := filepath.Join(root, "foreign")
	run(t, root, "git", "init", "-b", "main", repo)
	run(t, repo, "git", "config", "user.name", "DIU Test")
	run(t, repo, "git", "config", "user.email", "diu@example.invalid")
	writeReleaseFixture(t, filepath.Join(repo, "FOREIGN.md"), "# foreign index\n")
	run(t, repo, "git", "add", "FOREIGN.md")
	run(t, repo, "git", "commit", "-m", "test: foreign index")
	return filepath.Join(repo, ".git", "index")
}

func TestTagScriptRefusesDirtyWorktree(t *testing.T) {
	repo, origin := newReleaseRepo(t)
	dirtyPath := filepath.Join(repo, "dirty.txt")
	if err := os.WriteFile(dirtyPath, []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := runTagScript(t, repo, origin, filepath.Join(t.TempDir(), "release.log"), "y\n")
	if err == nil || !strings.Contains(output, "dirty worktree") {
		t.Fatalf("expected dirty worktree error, got %v\n%s", err, output)
	}
}

func TestTagScriptStopsWhenPreviewFails(t *testing.T) {
	repo, origin := newReleaseRepo(t)
	logPath := filepath.Join(t.TempDir(), "release.log")

	output, err := runTagScript(t, repo, origin, logPath, "y\n", "PREVIEW_EXIT=23")
	if err == nil || !strings.Contains(output, "release validation failed") {
		t.Fatalf("expected validation error, got %v\n%s", err, output)
	}
	if remoteTagExists(origin, "v0.2.0") {
		t.Fatal("release tag was pushed after failed validation")
	}
}

func TestTagScriptCancelsWithoutCreatingTag(t *testing.T) {
	repo, origin := newReleaseRepo(t)
	logPath := filepath.Join(t.TempDir(), "release.log")

	output, err := runTagScript(t, repo, origin, logPath, "n\n")
	if err != nil || !strings.Contains(output, "cancelled") {
		t.Fatalf("expected cancellation, got %v\n%s", err, output)
	}
	if remoteTagExists(origin, "v0.2.0") {
		t.Fatal("release tag was pushed after cancellation")
	}
}

func TestTagScriptRejectsInvalidSVUVersion(t *testing.T) {
	repo, origin := newReleaseRepo(t)
	logPath := filepath.Join(t.TempDir(), "release.log")

	output, err := runTagScript(t, repo, origin, logPath, "y\n", "SVU_VERSION=latest")
	if err == nil || !strings.Contains(output, "invalid semantic version") {
		t.Fatalf("expected semantic version error, got %v\n%s", err, output)
	}
}

func newReleaseRepo(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "work")
	run(t, root, "git", "init", "--bare", origin)
	run(t, root, "git", "init", "-b", "main", repo)
	run(t, repo, "git", "config", "user.name", "DIU Test")
	run(t, repo, "git", "config", "user.email", "diu@example.invalid")
	writeReleaseFixture(t, filepath.Join(repo, "README.md"), "# release fixture\n")
	run(t, repo, "git", "add", "README.md")
	run(t, repo, "git", "commit", "-m", "feat: initial release")
	run(t, repo, "git", "remote", "add", "origin", origin)
	run(t, repo, "git", "push", "-u", "origin", "main")
	return repo, origin
}

func runTagScript(
	t *testing.T,
	repo string,
	origin string,
	logPath string,
	input string,
	extraEnv ...string,
) (string, error) {
	t.Helper()

	fakeBin := t.TempDir()
	writeReleaseTools(t, fakeBin)
	cmd := exec.Command("/bin/sh", filepath.Join(projectRoot(t), "scripts", "tag.sh"))
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(isolatedGitEnv(), releaseEnv(fakeBin, origin, logPath, extraEnv...)...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func releaseEnv(fakeBin, origin, logPath string, extraEnv ...string) []string {
	env := []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"RELEASE_LOG=" + logPath,
		"RELEASE_ORIGIN=" + origin,
	}
	return append(env, extraEnv...)
}

func writeReleaseFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeReleaseTools(t *testing.T, dir string) {
	t.Helper()

	writeExecutable(t, filepath.Join(dir, "svu"), `#!/bin/sh
test "$1" = "next" || exit 2
printf '%s\n' "${SVU_VERSION:-v0.2.0}"
`)
	writeExecutable(t, filepath.Join(dir, "mise"), `#!/bin/sh
printf '%s\n' "$*" >> "$RELEASE_LOG"
exit "${PREVIEW_EXIT:-0}"
`)
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func remoteTagExists(origin, tag string) bool {
	cmd := exec.Command("git", "--git-dir", origin, "show-ref", "--verify", "--quiet", "refs/tags/"+tag)
	cmd.Env = isolatedGitEnv()
	return cmd.Run() == nil
}
