#!/usr/bin/env bash
# scripts/update-pins.sh — rewrite versions.lock.yaml's SHAs from each pinned
# sibling's current origin/main.
#
# The single most important property of this script: the source of truth is
# always a sibling's `origin/main`, never its local HEAD. That is what makes
# "a pin recorded from a commit nobody pushed" structurally impossible to
# reintroduce -- a SHA read from origin/main is by definition fetchable by
# CI. `--from-head`/`--ref` are explicit, guarded escape hatches; they still
# verify reachability from origin/main before writing anything.
#
# Full contract: `update-pins.sh --help`, or see
# plan/notes/bootstrap-script-design.md's "Companion contract: update-pins.sh"
# section (go-module-ci-pinning plan) and, once authored,
# docs/mf-standards/versions-lockfile.md.
#
# Shared by copy with the other carrier repos, together with the sourced
# scripts/lib/lockfile.sh reader (also used by checkout-deps.sh and
# link-siblings.sh), the same distribution model scripts/link-siblings.sh
# already uses. Do not diverge the copies without updating all of them.

set -euo pipefail

err() { printf 'update-pins: %s\n' "$*" >&2; }

print_help() {
	cat <<'EOF'
Usage: update-pins.sh [REPO...] [OPTIONS]

Rewrite versions.lock.yaml's SHAs from each sibling's current origin/main.
With no REPO arguments, updates every lockfile key. Idempotent.

OPTIONS
  --from-head REPO      Pin REPO's local HEAD instead of origin/main. Refuses
                        unless `git rev-list --count origin/main..HEAD` is 0.
  --ref REPO=<committish>
                        Pin an explicit ref, verified reachable from
                        origin/main via `git merge-base --is-ancestor`.
  --dry-run             Print the diff that would be written; write nothing.
  --lockfile PATH       Default: <repo-toplevel>/versions.lock.yaml
  -h, --help             Print this contract and exit 0.

SIBLING ROOT RESOLUTION
  1. $MODULEFORGE_SIBLINGS_DIR      (same variable link-siblings.sh defines)
  2. dirname(`git rev-parse --show-toplevel`)

  Pin bumping needs a local checkout to read origin/main from -- there is no
  --into here (contrast checkout-deps.sh, which can materialize one).

EXIT CODES
  0  Lockfile updated, or already current (no-op).
  2  Usage error.
  3  Precondition failure: lockfile missing or malformed (strict, fail-closed
     grammar shared with checkout-deps.sh); sibling checkout not found; the
     sibling's `origin` remote does not match the lockfile's host/owner (or
     the key's overrides); origin unreachable; --from-head on an unpushed
     HEAD; --ref not reachable from origin/main.
EOF
}

# ---------------------------------------------------------------------------
# Lockfile parsing: the one shared reader, scripts/lib/lockfile.sh (see that
# file, and checkout-deps.sh, for why this is a plain regex scan and not a YAML
# library, and for the strict fail-closed grammar).
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/lockfile.sh
. "$SCRIPT_DIR/lib/lockfile.sh"

# update_pin_line FILE KEY NEW_SHA COMMENT -- rewrite exactly KEY's line,
# keeping its existing key/padding prefix byte-for-byte and replacing only
# the SHA and trailing provenance comment. Every other line is copied
# through untouched. KEY is matched literally (never interpolated into a
# regex), must pass valid_repo, and only the first matching line is rewritten.
update_pin_line() {
	local file="$1" key="$2" new_sha="$3" comment="$4"
	local tmp replaced=0 line prefix rest

	valid_repo "$key" || {
		err "refusing to rewrite a pin whose key is not a plain repository name"
		exit 3
	}
	[[ "$new_sha" =~ ^[0-9a-f]{40}$ ]] || {
		err "internal error: new SHA for $key is not 40 lower-case hex"
		exit 3
	}
	tmp="$(mktemp)"

	while IFS= read -r line || [ -n "$line" ]; do
		if [ "$replaced" -eq 0 ] && [[ "$line" == "  $key:"* ]]; then
			rest="${line#"  $key:"}"
			if [[ "$rest" =~ ^([[:space:]]+)[0-9a-fA-F]{40} ]]; then
				prefix="  $key:${BASH_REMATCH[1]}"
				printf '%s%s  # %s\n' "$prefix" "$new_sha" "$comment" >>"$tmp"
				replaced=1
				continue
			fi
		fi
		printf '%s\n' "$line" >>"$tmp"
	done <"$file"

	if [ "$replaced" -ne 1 ]; then
		rm -f "$tmp"
		err "internal error: could not find $key's line in $file"
		exit 3
	fi
	mv "$tmp" "$file"
}

# origin_matches_lockfile KEY DIR -- does DIR's `origin` URL (as git would use
# it, after insteadOf rewriting) point at the lockfile's host/owner for KEY
# (overrides.<key>.host/owner applied)? Mismatch or an unparseable URL sets
# ORIGIN_MISMATCH to a message that never contains the raw URL (it may carry
# credentials) and returns 1. Host and owner compare case-insensitively.
ORIGIN_MISMATCH=""
origin_matches_lockfile() {
	local key="$1" dir="$2" url host owner
	local want_host="${LF_OV_HOST[$key]:-$LF_HOST}" want_owner="${LF_OV_OWNER[$key]:-$LF_OWNER}"
	url="$(git -C "$dir" remote get-url origin 2>/dev/null)" || {
		ORIGIN_MISMATCH="$key: $dir has no 'origin' remote to compare with the lockfile"
		return 1
	}
	# scp-like and ssh://: [ssh://]user@host(:|/)owner/repo ; https/http: [user@]host[:port]/owner/repo
	if [[ "$url" =~ ^(ssh://)?[A-Za-z0-9._-]+@([A-Za-z0-9.-]+)(:[0-9]+)?[:/]([A-Za-z0-9._-]+)/[A-Za-z0-9._-]+(\.git)?/?$ ]]; then
		host="${BASH_REMATCH[2]}"
		owner="${BASH_REMATCH[4]}"
	elif [[ "$url" =~ ^https?://([^/@]+@)?([A-Za-z0-9.-]+)(:[0-9]+)?/([A-Za-z0-9._-]+)/[A-Za-z0-9._-]+(\.git)?/?$ ]]; then
		host="${BASH_REMATCH[2]}"
		owner="${BASH_REMATCH[4]}"
	else
		ORIGIN_MISMATCH="$key: cannot parse the host/owner of $dir's 'origin' URL; expected ${want_host}/${want_owner} per the lockfile"
		return 1
	fi
	if [ "${host,,}" != "${want_host,,}" ] || [ "${owner,,}" != "${want_owner,,}" ]; then
		ORIGIN_MISMATCH="$key: $dir's 'origin' is ${host}/${owner} but the lockfile says ${want_host}/${want_owner}; refusing to record a pin from a different remote (a fork?)"
		return 1
	fi
	return 0
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

POSITIONAL_REPOS=()
FROM_HEAD_REPOS=()
declare -A REF_OVERRIDES=()
DRY_RUN=0
LOCKFILE=""

while [ $# -gt 0 ]; do
	case "$1" in
	--from-head)
		[ $# -ge 2 ] || {
			err "--from-head requires REPO"
			exit 2
		}
		FROM_HEAD_REPOS+=("$2")
		shift 2
		;;
	--ref)
		[ $# -ge 2 ] || {
			err "--ref requires REPO=<committish>"
			exit 2
		}
		case "$2" in
		*=*)
			REF_OVERRIDES["${2%%=*}"]="${2#*=}"
			;;
		*)
			err "--ref requires REPO=<committish>, got: $2"
			exit 2
			;;
		esac
		shift 2
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	--lockfile)
		[ $# -ge 2 ] || {
			err "--lockfile requires PATH"
			exit 2
		}
		LOCKFILE="$2"
		shift 2
		;;
	-h | --help)
		print_help
		exit 0
		;;
	--*)
		err "unknown option: $1 (see --help)"
		exit 2
		;;
	*)
		POSITIONAL_REPOS+=("$1")
		shift
		;;
	esac
done

R="$(git rev-parse --path-format=absolute --show-toplevel 2>/dev/null)" || {
	err "not inside a git repo"
	exit 3
}
LOCKFILE="${LOCKFILE:-$R/versions.lock.yaml}"

read_lockfile "$LOCKFILE"

# Positional REPO args restrict the run; validate against the lockfile's own
# key set (unknown names are a usage error, same as checkout-deps.sh --repos).
if [ "${#POSITIONAL_REPOS[@]}" -gt 0 ]; then
	TARGET_REPOS=()
	for r in "${POSITIONAL_REPOS[@]}"; do
		if [ -z "${LF_SHA[$r]+x}" ]; then
			err "unknown repo: '$r' (not in $LOCKFILE)"
			exit 2
		fi
		TARGET_REPOS+=("$r")
	done
else
	TARGET_REPOS=("${LF_KEYS[@]}")
fi

for r in "${FROM_HEAD_REPOS[@]}"; do
	if [ -z "${LF_SHA[$r]+x}" ]; then
		err "--from-head: unknown repo '$r' (not in $LOCKFILE)"
		exit 2
	fi
done
for r in "${!REF_OVERRIDES[@]}"; do
	if [ -z "${LF_SHA[$r]+x}" ]; then
		err "--ref: unknown repo '$r' (not in $LOCKFILE)"
		exit 2
	fi
done

if [ -n "${MODULEFORGE_SIBLINGS_DIR:-}" ]; then
	S="${MODULEFORGE_SIBLINGS_DIR}"
else
	S="$(dirname "$R")"
fi

is_from_head() {
	local r="$1"
	for x in "${FROM_HEAD_REPOS[@]}"; do
		[ "$x" = "$r" ] && return 0
	done
	return 1
}

updated=0
unchanged=0

for key in "${TARGET_REPOS[@]}"; do
	dir="$S/$key"
	if [ ! -d "$dir" ]; then
		err "sibling checkout not found: $dir -- pin bumping reads origin/main from a local checkout, so it must already exist."
		exit 3
	fi

	# The lockfile's host/owner (not whatever the sibling's origin happens to be)
	# define where pins come from: check before any network call.
	if ! origin_matches_lockfile "$key" "$dir"; then
		err "$ORIGIN_MISMATCH"
		exit 3
	fi

	git -C "$dir" fetch --quiet origin main

	old="${LF_SHA[$key]}"

	if [ -n "${REF_OVERRIDES[$key]:-}" ]; then
		ref="${REF_OVERRIDES[$key]}"
		new="$(git -C "$dir" rev-parse "$ref" 2>/dev/null)" || {
			err "$key: cannot resolve ref '$ref' in $dir"
			exit 3
		}
		if ! git -C "$dir" merge-base --is-ancestor "$new" origin/main 2>/dev/null; then
			err "$key: '$ref' (${new:0:8}) is not reachable from origin/main -- push it first."
			exit 3
		fi
	elif is_from_head "$key"; then
		ahead="$(git -C "$dir" rev-list --count origin/main..HEAD)"
		if [ "$ahead" != "0" ]; then
			err "$key is $ahead commit(s) ahead of origin/main; push before pinning."
			exit 3
		fi
		new="$(git -C "$dir" rev-parse HEAD)"
	else
		new="$(git -C "$dir" rev-parse origin/main)"
	fi

	if [ "$new" = "$old" ]; then
		printf '%s unchanged (origin/main has not moved; did you push?)\n' "$key"
		unchanged=$((unchanged + 1))
		continue
	fi

	comment="$(git -C "$dir" log -1 --format='%cs %s' "$new")"

	suffix=""
	if git -C "$dir" cat-file -e "${old}^{commit}" 2>/dev/null; then
		count="$(git -C "$dir" rev-list --count "${old}..${new}" 2>/dev/null || true)"
		[ -n "$count" ] && suffix="  (+${count} commits)"
	fi

	if [ "$DRY_RUN" -eq 1 ]; then
		printf '%s  %s \xe2\x86\x92 %s%s  (dry-run, not written)\n' "$key" "${old:0:8}" "${new:0:8}" "$suffix"
	else
		update_pin_line "$LOCKFILE" "$key" "$new" "$comment"
		printf '%s  %s \xe2\x86\x92 %s%s\n' "$key" "${old:0:8}" "${new:0:8}" "$suffix"
	fi
	updated=$((updated + 1))
done

printf '%d updated, %d unchanged.\n' "$updated" "$unchanged"
exit 0
