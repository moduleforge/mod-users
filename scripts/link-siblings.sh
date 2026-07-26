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
# SIBLINGS below is derived from api/go.mod's replace directives:
# core-model => ../../mod-core/model, core-api => ../../mod-core/api,
# audit-model => ../../mod-audit/model, audit-api => ../../mod-audit/api,
# authz-model => ../../mod-authz/model, authz-api => ../../mod-authz/api.
# (The mod-users/model => ../model replace is intra-repo and needs no
# symlink.)
#
# Env:
#   MODULEFORGE_SIBLINGS_DIR — override the aggregate directory that holds
#     mod-core/, mod-users/, mod-authz/, mod-audit/, mfgen/. Defaults to the
#     parent directory of this repo's main checkout.

set -euo pipefail

# TRANSIENT: mod-users will carry a versions.lock.yaml (Phase 3 of the
# go-module-ci-pinning plan). Phase 3 replaces this literal array with a
# read of the lockfile's `pins:` keys — that becomes the single generation
# source; this is the one-phase gap before it exists, not a regression.
SIBLINGS=(mod-core mod-audit mod-authz)

# git rev-parse --git-common-dir always points at the *main* checkout's .git
# directory, even when invoked from inside a linked worktree — this is what
# lets the script find the aggregate root and the one shared `worktrees/`
# directory regardless of where it is run from.
GIT_COMMON_DIR="$(git rev-parse --path-format=absolute --git-common-dir)"
MAIN_REPO_ROOT="$(dirname "$GIT_COMMON_DIR")"
WORKTREES_DIR="$MAIN_REPO_ROOT/worktrees"

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
