#!/bin/sh
# AGENTS.md is the only place for agent rules; per-tool files just import it.
set -e
for f in CLAUDE.md GEMINI.md; do
	if [ "$(cat "$f")" != "@AGENTS.md" ]; then
		echo "$f must contain only '@AGENTS.md' — put rules in AGENTS.md" >&2
		exit 1
	fi
done
