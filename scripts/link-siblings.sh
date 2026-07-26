#!/usr/bin/env bash
# scripts/link-siblings.sh — plant compatibility symlinks so this repo's
# sibling-relative paths (api/go.mod's replace directives) resolve
# correctly no matter how deeply a Flow task or plan worktree nests this
# checkout below the moduleforge aggregate root.
#
# Canonical explanation of the mechanism, why it exists, and the full
# wiring convention (make preflight as a prerequisite of build/test/lint):
# docs/mf-standards/building-common.md#building-inside-a-task-worktree
#
# SIBLINGS is read from versions.lock.yaml's `pins:` keys rather than
# hand-maintained here -- the lockfile is the single generation source for
# this repo's sibling set (go-module-ci-pinning plan, decision D3), so this
# script and checkout-deps.sh/update-pins.sh can never drift apart on which
# siblings exist. The lockfile's key set matches api/go.mod's replace
# directives: core-model => ../../mod-core/model, core-api =>
# ../../mod-core/api, audit-model => ../../mod-audit/model, audit-api =>
# ../../mod-audit/api, authz-model => ../../mod-authz/model, authz-api =>
# ../../mod-authz/api. (The mod-users/model => ../model replace is
# intra-repo and needs no symlink, and correctly has no lockfile key.)
#
# Env:
#   MODULEFORGE_SIBLINGS_DIR — override the aggregate directory that holds
#     mod-core/, mod-users/, mod-authz/, mod-audit/, mfgen/. Defaults to the
#     parent directory of this repo's main checkout.

set -euo pipefail

# git rev-parse --git-common-dir always points at the *main* checkout's .git
# directory, even when invoked from inside a linked worktree — this is what
# lets the script find the aggregate root and the one shared `worktrees/`
# directory regardless of where it is run from.
GIT_COMMON_DIR="$(git rev-parse --path-format=absolute --git-common-dir)"
MAIN_REPO_ROOT="$(dirname "$GIT_COMMON_DIR")"
WORKTREES_DIR="$MAIN_REPO_ROOT/worktrees"

# versions.lock.yaml lives at the main checkout's root regardless of how
# deep this invocation is nested (same reasoning as MAIN_REPO_ROOT above) --
# a worktree does not carry its own copy of the lockfile.
LOCKFILE="$MAIN_REPO_ROOT/versions.lock.yaml"
if [ ! -f "$LOCKFILE" ]; then
	echo "link-siblings: ERROR: lockfile not found: $LOCKFILE" >&2
	exit 1
fi

# Pin lines are the only lines indented by exactly two spaces (host/owner/
# lockfileVersion are top-level, zero-indent) -- the same plain regex scan
# checkout-deps.sh and update-pins.sh use, deliberately not a YAML library.
declare -a SIBLINGS
while IFS= read -r line; do
	if [[ "$line" =~ ^\ \ ([A-Za-z0-9._-]+):[[:space:]]+[0-9a-fA-F]{40} ]]; then
		SIBLINGS+=("${BASH_REMATCH[1]}")
	fi
done <"$LOCKFILE"

if [ "${#SIBLINGS[@]}" -eq 0 ]; then
	echo "link-siblings: ERROR: no pins found in $LOCKFILE" >&2
	exit 1
fi

if [ -n "${MODULEFORGE_SIBLINGS_DIR:-}" ]; then
	AGGREGATE_DIR="$(cd "$MODULEFORGE_SIBLINGS_DIR" && pwd)"
else
	AGGREGATE_DIR="$(dirname "$MAIN_REPO_ROOT")"
fi

# The current worktree's own toplevel — whatever depth it is nested at
# (plain checkout, task worktree, plan worktree, or any depth beyond).
# Its parent directory is where `../<sibling>` needs to resolve for *this*
# invocation, discovered fresh rather than assumed.
CURRENT_TOPLEVEL="$(git rev-parse --path-format=absolute --show-toplevel)"
CURRENT_PARENT_DIR="$(dirname "$CURRENT_TOPLEVEL")"

# link_siblings_into DIR — ensure DIR/<sibling> resolves to
# AGGREGATE_DIR/<sibling> for every sibling, either because DIR already IS
# AGGREGATE_DIR (real directories, nothing to do) or via a symlink.
link_siblings_into() {
	local dir="$1"
	mkdir -p "$dir"

	for sib in "${SIBLINGS[@]}"; do
		local target="$AGGREGATE_DIR/$sib"
		local link="$dir/$sib"

		if [ "$link" = "$target" ]; then
			# dir IS the aggregate directory itself (e.g. a plain
			# top-level checkout's toplevel is one level below the
			# aggregate) — the real sibling directory already lives
			# here; nothing to link.
			continue
		fi

		if [ ! -d "$target" ]; then
			echo "link-siblings: WARNING: $target not found; skipping $sib in $dir" >&2
			continue
		fi

		if [ -L "$link" ]; then
			local current
			current="$(readlink "$link")"
			if [ "$current" = "$target" ]; then
				echo "link-siblings: $link -> $target (already correct)"
				continue
			fi
			rm "$link"
		elif [ -e "$link" ]; then
			echo "link-siblings: ERROR: $link exists and is not a symlink; refusing to overwrite" >&2
			exit 1
		fi

		ln -s "$target" "$link"
		echo "link-siblings: linked $link -> $target"
	done
}

# Pre-seed the shared worktrees/ directory (helps every future one-level
# task worktree, per the original mechanism)...
link_siblings_into "$WORKTREES_DIR"

# ...and, depth-agnostically, fix up wherever this invocation is actually
# running from, whatever depth that turns out to be.
if [ "$CURRENT_PARENT_DIR" != "$WORKTREES_DIR" ]; then
	link_siblings_into "$CURRENT_PARENT_DIR"
fi
