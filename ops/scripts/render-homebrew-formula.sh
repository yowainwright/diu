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
	url_pattern='^(https://|file:///)[0-9A-Za-z._~:/%+-]+$'
	[[ "$archive_base_url" =~ $url_pattern ]] || die "invalid archive base URL: $archive_base_url"
	[[ -f "$checksums_file" ]] || die "checksums file not found: $checksums_file"
}

archive_checksum() {
	local architecture="${1:?}"
	local archive="diu_${version}_darwin_${architecture}.tar.gz"
	local checksum filename matched=""
	while read -r checksum filename; do
		[[ "$filename" == "$archive" ]] || continue
		[[ -z "$matched" && "$checksum" =~ $sha_pattern ]] || die "invalid or duplicate checksum for $archive"
		matched="$checksum"
	done <"$checksums_file"
	[[ -n "$matched" ]] || die "missing checksum for $archive"
	printf '%s' "$matched"
}

write_formula() {
	cat <<FORMULA
# frozen_string_literal: true

class Diu < Formula
  desc "Track package-manager and global CLI usage"
  homepage "https://github.com/yowainwright/diu"
  license "MIT"

  on_macos do
    on_arm do
      url "${archive_base_url}/diu_${version}_darwin_arm64.tar.gz"
      sha256 "${arm64_sha256}"
    end

    on_intel do
      url "${archive_base_url}/diu_${version}_darwin_amd64.tar.gz"
      sha256 "${amd64_sha256}"
    end
  end

  depends_on macos: :monterey

  def install
    bin.install "diu"
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
	[[ $# -eq 3 ]] || die "usage: $0 <version> <archive-base-url> <checksums-file>"
	version="${1-}"
	version="${version#v}"
	archive_base_url="${2:?}"
	archive_base_url="${archive_base_url%/}"
	checksums_file="${3:?}"
	validate_formula_args "$@"
	arm64_sha256="$(archive_checksum arm64)"
	amd64_sha256="$(archive_checksum amd64)"
	write_formula
}

main "$@"
