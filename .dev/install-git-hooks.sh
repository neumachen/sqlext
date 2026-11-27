#!/usr/bin/env bash
# Installs, in this clone only, a pre-commit hook that runs
# `.dev/lint.sh check --staged`. With --uninstall it removes that hook again.
# It never replaces a pre-commit hook it did not install and does not change
# Git configuration, so other hooks (such as Git LFS) keep working.
set -euo pipefail

MARKER='# sqlext pre-commit hook (installed by .dev/install-git-hooks.sh)'

die() {
	printf 'install-git-hooks: %s\n' "$*" >&2
	exit 1
}

root=$(git rev-parse --show-toplevel)
cd "$root"

if hooks_path=$(git config --get core.hooksPath); then
	die "core.hooksPath is set to '$hooks_path', so Git does not run hooks from .git/hooks; add '.dev/lint.sh check --staged' to the pre-commit hook in that directory instead"
fi

hook=$(git rev-parse --git-path hooks/pre-commit)

case "${1:-}" in
"")
	if [ -e "$hook" ] && ! grep -qF "$MARKER" "$hook"; then
		die "$hook already exists and was not installed by this script; it is unchanged. To add the check, call .dev/lint.sh check --staged from it"
	fi
	mkdir -p "$(dirname "$hook")"
	cat >"$hook" <<EOF
#!/bin/sh
$MARKER
# Checks the staged Go changes for formatting and new lint issues.
script="\$(git rev-parse --show-toplevel)/.dev/lint.sh"
if [ ! -x "\$script" ]; then
	echo "pre-commit: .dev/lint.sh is not in this checkout; skipping the lint check" >&2
	exit 0
fi
exec "\$script" check --staged
EOF
	chmod +x "$hook"
	echo "installed $hook"
	;;
--uninstall)
	if [ -f "$hook" ] && grep -qF "$MARKER" "$hook"; then
		rm "$hook"
		echo "removed $hook"
	else
		echo "no pre-commit hook from this script is installed"
	fi
	;;
*) die "usage: $0 [--uninstall]" ;;
esac
