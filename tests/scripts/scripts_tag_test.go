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
	run := newTagScriptRun(t, repo, origin)

	output, err := runTagScript(t, run)
	if err != nil {
		t.Fatalf("tag script failed: %v\n%s", err, output)
	}
	assertRemoteTagExists(t, origin, "v0.2.0")
	assertReleasePreviewLog(t, run.logPath)
}

func newTagScriptRun(t *testing.T, repo, origin string) tagScriptRun {
	t.Helper()

	return tagScriptRun{
		repo:    repo,
		origin:  origin,
		logPath: filepath.Join(t.TempDir(), "release.log"),
		input:   "y\n",
	}
}

func assertRemoteTagExists(t *testing.T, origin, tag string) {
	t.Helper()

	if !remoteTagExists(origin, tag) {
		t.Fatalf("expected %s to be pushed", tag)
	}
}

func assertReleasePreviewLog(t *testing.T, logPath string) {
	t.Helper()

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

	logPath := filepath.Join(t.TempDir(), "release.log")
	run := tagScriptRun{
		repo:    repo,
		origin:  origin,
		logPath: logPath,
		input:   "y\n",
	}
	output, err := runTagScript(t, run)
	hasDirtyWorktreeError := err != nil && strings.Contains(output, "dirty worktree")
	if !hasDirtyWorktreeError {
		t.Fatalf("expected dirty worktree error, got %v\n%s", err, output)
	}
}

func TestTagScriptStopsWhenPreviewFails(t *testing.T) {
	repo, origin := newReleaseRepo(t)
	logPath := filepath.Join(t.TempDir(), "release.log")

	run := tagScriptRun{
		repo:     repo,
		origin:   origin,
		logPath:  logPath,
		input:    "y\n",
		extraEnv: []string{"PREVIEW_EXIT=23"},
	}
	output, err := runTagScript(t, run)
	hasValidationError := err != nil && strings.Contains(output, "release validation failed")
	if !hasValidationError {
		t.Fatalf("expected validation error, got %v\n%s", err, output)
	}
	if remoteTagExists(origin, "v0.2.0") {
		t.Fatal("release tag was pushed after failed validation")
	}
}

func TestTagScriptCancelsWithoutCreatingTag(t *testing.T) {
	repo, origin := newReleaseRepo(t)
	logPath := filepath.Join(t.TempDir(), "release.log")

	run := tagScriptRun{
		repo:    repo,
		origin:  origin,
		logPath: logPath,
		input:   "n\n",
	}
	output, err := runTagScript(t, run)
	cancelled := err == nil && strings.Contains(output, "cancelled")
	if !cancelled {
		t.Fatalf("expected cancellation, got %v\n%s", err, output)
	}
	if remoteTagExists(origin, "v0.2.0") {
		t.Fatal("release tag was pushed after cancellation")
	}
}

func TestTagScriptRejectsInvalidSVUVersion(t *testing.T) {
	repo, origin := newReleaseRepo(t)
	logPath := filepath.Join(t.TempDir(), "release.log")

	run := tagScriptRun{
		repo:     repo,
		origin:   origin,
		logPath:  logPath,
		input:    "y\n",
		extraEnv: []string{"SVU_VERSION=latest"},
	}
	output, err := runTagScript(t, run)
	hasVersionError := err != nil && strings.Contains(output, "invalid semantic version")
	if !hasVersionError {
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

type tagScriptRun struct {
	repo     string
	origin   string
	logPath  string
	input    string
	extraEnv []string
}

func runTagScript(t *testing.T, run tagScriptRun) (string, error) {
	t.Helper()

	fakeBin := t.TempDir()
	writeReleaseTools(t, fakeBin)
	cmd := exec.Command("/bin/sh", filepath.Join(projectRoot(t), "ops", "scripts", "tag.sh"))
	cmd.Dir = run.repo
	cmd.Stdin = strings.NewReader(run.input)
	cmd.Env = append(isolatedGitEnv(), releaseEnv(fakeBin, run)...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func releaseEnv(fakeBin string, run tagScriptRun) []string {
	path := "PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH")
	env := []string{
		path,
		"RELEASE_LOG=" + run.logPath,
		"RELEASE_ORIGIN=" + run.origin,
	}
	return append(env, run.extraEnv...)
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
