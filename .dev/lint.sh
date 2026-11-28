#!/usr/bin/env bash
# Formatting and lint checks with the golangci-lint version and .golangci.yml
# that CI uses. See README.md, "Formatting and linting".
#
#   .dev/lint.sh check            whole repository: formatting plus lint, as CI
#   .dev/lint.sh check --changed  only problems in Go changes since HEAD
#                                 (staged, unstaged and untracked)
#   .dev/lint.sh check --staged   only problems in the staged change, checked on
#                                 a snapshot of the index (the pre-commit hook)
#   .dev/lint.sh fmt              format the Go files changed since HEAD in the
#                                 working tree; never touches the index
#
# The checks only read files. golangci-lint compiles packages to analyse them
# but runs no tests and opens no database. Exit status: 0 clean, 1 problems
# found, 2 usage error, 3 golangci-lint missing.
set -euo pipefail

# CI uses this version for lint and formatting: .github/workflows/code-quality.yml
# reads it from this line, so keep it in the form EXPECTED_VERSION=<x.y.z>.
EXPECTED_VERSION=2.15.0

# Work from the physical path: golangci-lint matches issues to changed lines
# by absolute path, and a symlinked path (such as /tmp on macOS) would make
# every issue look unrelated to the change.
root=$(cd "$(git rev-parse --show-toplevel)" && pwd -P)
cd "$root"

die() {
	printf 'lint: %s\n' "$*" >&2
	exit 2
}

require_linter() {
	if ! command -v golangci-lint >/dev/null 2>&1; then
		cat >&2 <<EOF
lint: golangci-lint is not on PATH. CI uses v$EXPECTED_VERSION; install it, for example:
  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$EXPECTED_VERSION
EOF
		exit 3
	fi
	local version
	version=$(golangci-lint version --short 2>/dev/null || true)
	if [ "${version#v}" != "$EXPECTED_VERSION" ]; then
		printf 'lint: warning: golangci-lint %s is installed, CI uses %s; results may differ\n' \
			"${version:-(unknown)}" "$EXPECTED_VERSION" >&2
	fi
}

# Go files changed since HEAD: staged, unstaged and untracked, excluding deletions.
changed_go_files() {
	{
		if git rev-parse --verify --quiet HEAD >/dev/null; then
			git diff --name-only --diff-filter=ACMR HEAD -- '*.go'
		else
			git diff --cached --name-only --diff-filter=ACMR -- '*.go'
		fi
		git ls-files --others --exclude-standard -- '*.go'
	} | sort -u
}

staged_go_files() { git diff --cached --name-only --diff-filter=ACMR -- '*.go'; }

# check_format FILE... reports formatting differences without writing.
check_format() {
	[ "$#" -gt 0 ] || return 0
	local diff
	diff=$(golangci-lint fmt --diff "$@" 2>&1) && return 0
	printf '%s\n' "$diff"
	printf 'lint: formatting differs in the files above; run .dev/lint.sh fmt, review, then stage the result\n' >&2
	return 1
}

# check_lint ARGS... runs the linters; ARGS select new issues where wanted.
check_lint() {
	golangci-lint run "$@" ./... && return 0
	printf 'lint: golangci-lint reported the issues above\n' >&2
	return 1
}

check_all() {
	local status=0
	golangci-lint fmt --diff ./... || {
		status=1
		printf 'lint: formatting differs in the files above; run golangci-lint fmt or .dev/lint.sh fmt\n' >&2
	}
	check_lint || status=1
	return "$status"
}

check_changed() {
	local files=()
	while IFS= read -r file; do files+=("$file"); done < <(changed_go_files)
	if [ "${#files[@]}" -eq 0 ]; then
		echo "lint: no changed Go files since HEAD" >&2
		return 0
	fi
	require_linter
	local status=0
	check_format "${files[@]}" || status=1
	if git rev-parse --verify --quiet HEAD >/dev/null; then
		check_lint --new-from-rev=HEAD || status=1
	else
		check_lint || status=1
	fi
	return "$status"
}

check_staged() {
	local files=()
	while IFS= read -r file; do files+=("$file"); done < <(staged_go_files)
	if [ "${#files[@]}" -eq 0 ]; then
		echo "lint: no staged Go changes" >&2
		return 0
	fi
	require_linter

	# Check exactly what will be committed: a copy of the index, so unstaged
	# edits neither hide nor cause problems. The working tree is not touched.
	# The copy lives at a stable per-user, per-repository path with its own
	# golangci-lint cache: the cache reuses results across directories, so
	# results cached for another copy would carry paths that do not match the
	# change and new issues would be filtered out.
	local tmp base snapshot patch
	tmp=$(cd "${TMPDIR:-/tmp}" && pwd -P)
	base="$tmp/sqlext-lint-$(id -u)-$(printf '%s' "$root" | cksum | cut -d' ' -f1)"
	mkdir -p "$base"
	chmod 700 "$base"
	if ! mkdir "$base/lock" 2>/dev/null; then
		printf 'lint: another staged check is running (remove %s if it is stale)\n' "$base/lock" >&2
		return 1
	fi
	snapshot="$base/index"
	patch="$base/staged.patch"
	# shellcheck disable=SC2064 # expand the paths now
	trap "rm -rf '$snapshot' '$patch'; rmdir '$base/lock'" EXIT
	rm -rf "$snapshot"
	mkdir "$snapshot"
	git checkout-index --all --prefix="$snapshot/"
	git diff --cached --no-color --no-ext-diff >"$patch"
	# vendor/ is ignored by git but used for offline builds; share it.
	if [ -d vendor ] && [ ! -e "$snapshot/vendor" ]; then
		ln -s "$root/vendor" "$snapshot/vendor"
	fi

	local status=0
	(cd "$snapshot" && check_format "${files[@]}") || status=1
	(cd "$snapshot" && GOLANGCI_LINT_CACHE="$base/cache" check_lint --new-from-patch="$patch") || status=1
	return "$status"
}

format_changed() {
	local files=()
	while IFS= read -r file; do files+=("$file"); done < <(changed_go_files)
	if [ "${#files[@]}" -eq 0 ]; then
		echo "lint: no changed Go files to format" >&2
		return 0
	fi
	require_linter
	golangci-lint fmt "${files[@]}"
	echo "lint: formatted ${#files[@]} changed Go file(s) in the working tree; nothing was staged" >&2
}

command=${1:-check}
scope=${2:-}
case "$command:$scope" in
check:)
	require_linter
	check_all
	;;
check:--changed)
	check_changed
	;;
check:--staged)
	check_staged
	;;
fmt:)
	format_changed
	;;
*) die "usage: $0 check [--changed|--staged] | fmt" ;;
esac
