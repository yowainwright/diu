#!/bin/sh
set -eu

RELEASE_BRANCH="${RELEASE_BRANCH:-main}"
RELEASE_REPOSITORY="${RELEASE_REPOSITORY:-yowainwright/diu}"

die() {
  printf 'release: %s\n' "$1" >&2
  exit 1
}

require_command() {
  name="$1"
  command -v "$name" >/dev/null 2>&1 || die "$name is required"
}

check_prerequisites() {
  require_command git
  require_command mise
  require_command svu
}

check_clean() {
  status="$(git status --porcelain)"
  [ -z "$status" ] || die "refusing to release a dirty worktree"
}

check_branch() {
  branch="$(git branch --show-current)"
  [ "$branch" = "$RELEASE_BRANCH" ] || die "releases must run from $RELEASE_BRANCH (on $branch)"
}

check_repository() {
  origin="$(git remote get-url origin)" || die "origin is unavailable"
  if [ -n "${RELEASE_ORIGIN:-}" ]; then
    [ "$origin" = "$RELEASE_ORIGIN" ] || die "refusing to release from $origin"
    return
  fi

  ssh_origin="git@github.com:$RELEASE_REPOSITORY.git"
  https_origin="https://github.com/$RELEASE_REPOSITORY"
  https_git_origin="$https_origin.git"
  case "$origin" in
    "$ssh_origin"|"$https_origin"|"$https_git_origin") ;;
    *) die "refusing to release from $origin" ;;
  esac
}

refresh_origin() {
  git fetch --quiet origin "$RELEASE_BRANCH" --tags || die "could not refresh origin/$RELEASE_BRANCH"
}

check_synced() {
  remote_ref="refs/remotes/origin/$RELEASE_BRANCH"
  head_sha="$(git rev-parse HEAD)"
  origin_sha="$(git rev-parse "$remote_ref")" || die "origin/$RELEASE_BRANCH is unavailable"
  [ "$head_sha" = "$origin_sha" ] || die "HEAD is not synchronized with origin/$RELEASE_BRANCH"
}

check_release_context() {
  check_clean
  check_branch
  check_repository
  refresh_origin
  check_synced
}

next_tag() {
  svu next || die "svu could not determine the next version"
}

validate_tag() {
  semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
  printf '%s\n' "$1" | grep -Eq "$semver" || die "invalid semantic version: $1"
}

check_new_tag() {
  if git show-ref --verify --quiet "refs/tags/$1"; then
    die "$1 already exists"
  fi
}

confirm_release() {
  commit="$(git rev-parse --short HEAD)"
  printf 'release %s from %s? [y/N] ' "$1" "$commit" >&2
  answer=""
  IFS= read -r answer || true
  case "$answer" in
    y|Y|yes|YES) return ;;
    *) printf 'release: cancelled\n' >&2; exit 0 ;;
  esac
}

validate_release() {
  mise run release-preview || die "release validation failed"
}

publish_tag() {
  tag="$1"
  message="Release $tag"
  git tag -a "$tag" -m "$message" || die "could not create $tag"
  git push origin "refs/tags/$tag" || die "push failed; $tag remains as a local tag"
}

main() {
  [ "$#" -eq 0 ] || die "usage: ops/scripts/tag.sh"
  check_prerequisites
  check_release_context
  tag="$(next_tag)"
  validate_tag "$tag"
  check_new_tag "$tag"
  confirm_release "$tag"
  validate_release
  check_release_context
  check_new_tag "$tag"
  publish_tag "$tag"
  printf 'release: pushed %s\n' "$tag"
}

main "$@"
