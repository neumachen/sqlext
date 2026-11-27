#!/usr/bin/env bash
# Claude Code Stop hook, enabled per clone from the git-ignored
# .claude/settings.json (see README.md, "Formatting and linting"): when a turn
# ends with Go changes since HEAD, check them with `.dev/lint.sh check
# --changed` and hand problems back to Claude. It only reads files.
#
# Exit 0 lets the turn end, 2 reports problems to Claude (once per stop; see
# stop_hook_active), and 1 shows a notice to the user without blocking.
set -uo pipefail

input=$(cat)
# Already continuing because of this hook: do not block again.
case "$input" in
*'"stop_hook_active":true'* | *'"stop_hook_active": true'*) exit 0 ;;
esac

cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0
[ -x .dev/lint.sh ] || exit 0

output=$(.dev/lint.sh check --changed 2>&1)
case $? in
0) exit 0 ;;
3)
	printf '%s\n' "$output" >&2
	exit 1
	;;
*)
	{
		echo "Formatting or lint problems in the Go changes since HEAD (.dev/lint.sh check --changed):"
		printf '%s\n' "$output"
	} >&2
	exit 2
	;;
esac
