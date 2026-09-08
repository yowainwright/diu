#!/usr/bin/env bash
set -euo pipefail

die() {
	echo "error: $*" >&2
	exit 1
}

validate_formula_args() {
	version_pattern='^[0-9]+[.][0-9]+[.][0-9]+([-+][0-9A-Za-z.-]+)?$'
	sha_pattern='^[0-9a-f]{64}$'
	[[ "$version" =~ $version_pattern ]] || die "invalid version: $1"
	case "$source_url" in
	https://* | file://*) ;;
	*) die "invalid source URL: $source_url" ;;
	esac
	[[ "$source_url" != *\"* ]] || die "source URL must not contain quotes"
	[[ "$sha256" =~ $sha_pattern ]] || die "invalid sha256: $sha256"
}

write_formula() {
	cat <<FORMULA
# frozen_string_literal: true

class Diu < Formula
  desc "Track package-manager and global CLI usage"
  homepage "https://github.com/yowainwright/diu"
  url "${source_url}"
  sha256 "${sha256}"
  license "MIT"
  head "https://github.com/yowainwright/diu.git", branch: "main"

  depends_on "go" => :build
  depends_on :macos

  def install
    ENV["CGO_ENABLED"] = "0"
    ENV["GOTOOLCHAIN"] = "local"

    ldflags = [
      "-s",
      "-w",
      "-X main.version=#{version}",
      "-X github.com/yowainwright/diu/internal/core.Version=#{version}",
    ].join(" ")

    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/diu"
  end

  def caveats
    <<~EOS
      DIU stores configuration in ~/.config/diu/config.json
      and execution data in ~/.local/share/diu.

      Quick start:
        diu setup
        diu scan
    EOS
  end

  test do
    ENV["HOME"] = testpath

    assert_match "diu #{version}", shell_output("#{bin}/diu --version")
    system bin/"diu", "--help"
    assert_match "\"version\"", shell_output("#{bin}/diu config list")
  end
end
FORMULA
} # noqa: LEG038 - keep the formula template together.

main() {
	[[ $# -eq 3 ]] || die "usage: $0 <version> <source-url> <sha256>"
	version="${1-}"
	version="${version#v}"
	source_url="${2-}"
	sha256="${3-}"
	validate_formula_args "$@"
	write_formula
}

main "$@"
