#!/usr/bin/env bash
# scripts/checkout-deps.sh — materialize or verify the sibling repo checkouts
# pinned in versions.lock.yaml.
#
# One script, two modes, no CI-vs-local branching:
#   --verify (default)  report pin-vs-HEAD drift for every pinned sibling;
#                        mutates nothing; always exits 0 unless the lockfile
#                        itself is missing/malformed (--strict opts into a
#                        gating exit code).
#   --pinned            clone every pinned sibling at its exact SHA into the
#                        sibling root. A full pre-flight pass classifies
#                        every target path before anything is touched; if any
#                        target is neither absent nor already-matching, the
#                        whole run aborts (exit 4) without mutating anything.
#
# Full contract: `checkout-deps.sh --help`, or see
# plan/notes/bootstrap-script-design.md's "checkout-deps.sh command-line
# contract" section (go-module-ci-pinning plan) and, once authored,
# docs/mf-standards/versions-lockfile.md.
#
# This is one of two carrier-repo copies (the other lives in mod-users/) --
# the two are kept byte-identical by convention, the same distribution model
# scripts/link-siblings.sh already uses. Do not diverge the two without
# updating both.
#
# Security: MODULEFORGE_READ_TOKEN, when set, authenticates over HTTPS via a
# per-invocation `-c http.extraheader=` rather than the remote URL or
# .git/config, and every line that touches it runs with `set +x` so it can
# never be echoed even if the caller has xtrace on.

set -euo pipefail

# ---------------------------------------------------------------------------
# Small helpers
# ---------------------------------------------------------------------------

err() { printf 'checkout-deps: %s\n' "$*" >&2; }
log() { [ "${QUIET:-0}" -eq 1 ] && return 0; printf '%s\n' "$*"; }

json_escape() {
	local s="$1"
	s="${s//\\/\\\\}"
	s="${s//\"/\\\"}"
	printf '%s' "$s"
}

print_help() {
	cat <<'EOF'
Usage: checkout-deps.sh [MODE] [OPTIONS]

Materialize or verify the sibling repo checkouts pinned in versions.lock.yaml.

MODES (mutually exclusive; --verify is the default)
  --verify        Report pin-vs-HEAD drift for each pinned sibling.
                  Mutates nothing. Default when no mode is given.
  --pinned        Clone each pinned sibling at its exact SHA into the sibling
                  root. Never mutates an existing checkout: if any target path
                  exists and does not already match its pin, the whole run
                  aborts before touching anything (exit 4).

OPTIONS
  --into DIR      Sibling root override. Highest-precedence resolution for
                  where siblings are read from (--verify) or written to
                  (--pinned). Created if absent under --pinned.
  --lockfile PATH Lockfile to read. Default: <repo-toplevel>/versions.lock.yaml
  --repos "A B"   Restrict the operation to a subset of lockfile keys.
                  Unknown key names are a usage error.
  --format text|json
                  Report format. Default: text. (--verify only.)
  --strict        Exit 1 if any sibling is not `ok`. (--verify only.)
                  Without it, drift is reported and the exit code stays 0,
                  because local checkouts deliberately float.
  --remote        Additionally verify each pinned SHA is reachable from its
                  remote's default branch. Downgrades `missing` from a
                  --strict failure to informational, since a CI runner
                  legitimately has no local siblings. (--verify only.)
  --depth N       Fetch depth for --pinned. Default 1; 0 means full history.
  --submodules    After cloning, run `git submodule update --init --recursive`
                  in each sibling. Off by default; no job uses it today.
                  (--pinned only.)
  -q, --quiet     Suppress per-repo progress; print only the summary.
  -h, --help      Print this contract and exit 0.

SIBLING ROOT RESOLUTION (both modes, in precedence order)
  1. --into DIR
  2. $MODULEFORGE_SIBLINGS_DIR      (same variable link-siblings.sh defines)
  3. dirname(`git rev-parse --show-toplevel`)

  On a GitHub Actions runner where the repo is checked out with
  `path: <repo>`, rule 3 yields $GITHUB_WORKSPACE, so siblings land at the
  same paths today's per-repo `actions/checkout` steps use. In a fresh local
  clone it yields the parent directory, i.e. the canonical flat sibling
  layout. One rule, no CI-vs-local branching.

ENVIRONMENT
  MODULEFORGE_SIBLINGS_DIR  Sibling root (precedence 2 above).
  MODULEFORGE_READ_TOKEN    If set, clone over HTTPS authenticated with this
                            token via a per-invocation `-c http.extraheader`.
                            Never written to .git/config, never echoed.
                            If unset, clone over SSH (git@<host>:<owner>/<repo>.git).

EXIT CODES
  0  Success. --verify produced a report (even if drift/missing was found);
     --pinned materialized every requested sibling.
  1  --strict only: at least one sibling is not `ok`.
  2  Usage error: unknown flag, conflicting modes, unknown --repos key.
  3  Precondition failure: lockfile missing or malformed; not inside a git
     repo; --pinned invoked from inside a Flow worktree (pins govern CI and
     fresh clones only -- use `make preflight` there); a pinned SHA cannot be
     fetched from its remote (almost always an unpushed local commit).
  4  --pinned only: refuse-to-mutate. One or more target paths exist and do
     not match their pin. Nothing was modified. Remedy: --into <dir>.

EXAMPLES
  ./scripts/checkout-deps.sh
      Drift report for the current aggregate. Changes nothing, exits 0.

  ./scripts/checkout-deps.sh --pinned
      Fresh-clone bootstrap: clone every pinned sibling next to this repo.
      Aborts if any already exists in a non-matching state.

  ./scripts/checkout-deps.sh --pinned --into /tmp/mftodo-pinned
      Isolated pinned tree, leaving the working aggregate untouched.

  ./scripts/checkout-deps.sh --verify --remote --strict
      CI `pins` job: assert every pin is fetchable from its remote.
EOF
}

# ---------------------------------------------------------------------------
# Lockfile parsing (no YAML library -- the schema is deliberately flat and
# line-oriented so this is a plain regex scan; see update-pins.sh, which
# depends on the same one-key-per-line shape for exact-line rewrites).
# ---------------------------------------------------------------------------

# Populates globals: LF_HOST, LF_OWNER, LF_KEYS (ordered array), LF_SHA (map).
read_lockfile() {
	local file="$1"
	if [ ! -f "$file" ]; then
		err "lockfile not found: $file"
		exit 3
	fi

	LF_HOST="$(awk -F': *' '/^host:/{print $2; exit}' "$file" | tr -d '[:space:]')"
	LF_OWNER="$(awk -F': *' '/^owner:/{print $2; exit}' "$file" | tr -d '[:space:]')"
	if [ -z "$LF_HOST" ] || [ -z "$LF_OWNER" ]; then
		err "malformed lockfile: missing host/owner: $file"
		exit 3
	fi

	LF_KEYS=()
	# Pin lines are the only lines indented by exactly two spaces (host/owner/
	# lockfileVersion are top-level, zero-indent); this alone disambiguates
	# them from the header comment block without tracking "after pins:" state.
	while IFS= read -r line; do
		if [[ "$line" =~ ^\ \ ([A-Za-z0-9._-]+):[[:space:]]+([0-9a-fA-F]{40}) ]]; then
			local key="${BASH_REMATCH[1]}" sha="${BASH_REMATCH[2]}"
			LF_KEYS+=("$key")
			LF_SHA["$key"]="$sha"
		fi
	done <"$file"

	if [ "${#LF_KEYS[@]}" -eq 0 ]; then
		err "malformed lockfile: no pins found: $file"
		exit 3
	fi
}

# ---------------------------------------------------------------------------
# Remote / auth
# ---------------------------------------------------------------------------

clone_url() {
	local key="$1"
	if [ "$TOKEN_SET" -eq 1 ]; then
		printf 'https://%s/%s/%s.git' "$LF_HOST" "$LF_OWNER" "$key"
	else
		printf 'git@%s:%s/%s.git' "$LF_HOST" "$LF_OWNER" "$key"
	fi
}

# git_fetch_auth DIR REMOTE REF -- fetch REF from REMOTE (a remote name or a
# raw URL) into the repo at DIR, authenticating via a per-invocation header
# when MODULEFORGE_READ_TOKEN is set. The token never touches the URL or
# .git/config. Every line that dereferences $MODULEFORGE_READ_TOKEN itself
# runs under `set +x` regardless of the caller's tracing state -- checking
# only the pre-computed $TOKEN_SET flag (never the raw variable) is what
# keeps this function's own `if` safe to trace.
git_fetch_auth() {
	local dir="$1" remote="$2" ref="$3"
	local was_xtrace=0
	case "$-" in *x*) was_xtrace=1 ;; esac

	if [ "$TOKEN_SET" -eq 1 ]; then
		set +x
		local auth_header
		auth_header="AUTHORIZATION: basic $(printf 'x-access-token:%s' "$MODULEFORGE_READ_TOKEN" | base64 | tr -d '\n')"
		local rc=0
		git -C "$dir" -c "http.extraheader=$auth_header" fetch --quiet "${DEPTH_ARGS[@]}" "$remote" "$ref" || rc=$?
		unset auth_header
		[ "$was_xtrace" -eq 1 ] && set -x
		return "$rc"
	fi

	git -C "$dir" fetch --quiet "${DEPTH_ARGS[@]}" "$remote" "$ref"
}

# check_remote_reachable KEY SHA -- attempt a throwaway shallow fetch of SHA
# into a scratch bare repo to prove it exists on the remote, without cloning
# a working tree anywhere. Used by --remote. Always cleans up after itself.
check_remote_reachable() {
	local key="$1" sha="$2" url tmp rc=0
	url="$(clone_url "$key")"
	tmp="$(mktemp -d)"
	git init --quiet --bare "$tmp" >/dev/null
	git_fetch_auth "$tmp" "$url" "$sha" >/dev/null 2>&1 || rc=1
	rm -rf "$tmp"
	return "$rc"
}

# ---------------------------------------------------------------------------
# --pinned
# ---------------------------------------------------------------------------

clone_pin() {
	local key="$1" pin="$2" dest="$3" url
	url="$(clone_url "$key")"
	git init --quiet "$dest"
	git -C "$dest" remote add origin "$url"
	if ! git_fetch_auth "$dest" origin "$pin"; then
		return 1
	fi
	git -C "$dest" checkout --quiet --detach FETCH_HEAD
	if [ "$SUBMODULES" -eq 1 ]; then
		git -C "$dest" submodule update --init --recursive
	fi
}

run_pinned() {
	# Worktree refusal: --pinned is CI/fresh-clone-only. Without this guard,
	# dirname(R) inside a Flow worktree would resolve into worktrees/ (or a
	# nested plan worktree), which the pre-flight pass below would then
	# correctly -- but confusingly -- reject as a pile of conflicts. Saying
	# the useful thing directly is worth a dedicated check.
	local common_dir main_root
	common_dir="$(git rev-parse --path-format=absolute --git-common-dir)"
	main_root="$(dirname "$common_dir")"
	if [ "$main_root" != "$R" ]; then
		err "--pinned refuses to run inside a Flow task/plan worktree ($R)."
		err "Worktree and local builds deliberately float against local HEADs; pins govern CI and fresh clones only."
		err "Use \`make preflight\` (scripts/link-siblings.sh) to resolve sibling paths inside a worktree instead."
		exit 3
	fi

	mkdir -p "$S"
	S="$(cd "$S" && pwd)"

	local -a to_create=() conflicts=()
	local key target pin

	for key in "${SELECTED_KEYS[@]}"; do
		target="$S/$key"
		pin="${LF_SHA[$key]}"

		if [ ! -e "$target" ]; then
			to_create+=("$key")
			continue
		fi
		if [ -L "$target" ]; then
			conflicts+=("$target -- is a symlink (expected a real git checkout or nothing)")
			continue
		fi
		if ! git -C "$target" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
			conflicts+=("$target -- exists and is not a git repository")
			continue
		fi

		local head_sha dirty
		head_sha="$(git -C "$target" rev-parse HEAD 2>/dev/null || true)"
		dirty="$(git -C "$target" status --porcelain 2>/dev/null || true)"
		if [ "$head_sha" = "$pin" ] && [ -z "$dirty" ]; then
			continue # satisfied -- left untouched
		fi
		local reason="HEAD=${head_sha:-none} (pin=${pin})"
		[ -n "$dirty" ] && reason="$reason, worktree dirty"
		conflicts+=("$target -- $reason")
	done

	if [ "${#conflicts[@]}" -gt 0 ]; then
		err "refusing to mutate -- the following paths exist and do not match their pin:"
		for c in "${conflicts[@]}"; do
			err "  $c"
		done
		err "Nothing was touched. Remedy: --into <dir> to materialize into an isolated tree."
		exit 4
	fi

	local cloned=0
	local satisfied=$((${#SELECTED_KEYS[@]} - ${#to_create[@]}))
	for key in "${to_create[@]}"; do
		pin="${LF_SHA[$key]}"
		target="$S/$key"
		log "checkout-deps: cloning $key @ ${pin:0:8} -> $target"
		if ! clone_pin "$key" "$pin" "$target"; then
			rm -rf "$target"
			err "failed to fetch $key @ $pin from its remote -- almost always because this SHA was never pushed to origin."
			err "Push the commit and retry, or run \`make pins.update REPOS=\"$key\"\` if the wrong SHA was pinned."
			exit 3
		fi
		cloned=$((cloned + 1))
	done

	log "checkout-deps: $cloned cloned, $satisfied already satisfied."
}

# ---------------------------------------------------------------------------
# --verify
# ---------------------------------------------------------------------------

CNT_OK=0
CNT_DRIFT=0
CNT_UNFETCHED=0
CNT_MISSING=0
# `unpinned` (a sibling on disk with no lockfile key) has no second source of
# truth to compare against in this script now that the lockfile itself is
# the sibling-set generation source (Requirement 5) -- there is nothing left
# to be "extra" relative to. The status stays in the vocabulary/report shape
# for forward compatibility but this script can never produce it.
CNT_UNPINNED=0

compute_status() {
	local key="$1" target pin
	target="$S/$key"
	pin="${LF_SHA[$key]}"

	if [ ! -e "$target" ] || ! git -C "$target" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		ST_STATUS["$key"]="missing"
		ST_HEAD["$key"]=""
		ST_DIRTY["$key"]="-"
		CNT_MISSING=$((CNT_MISSING + 1))
		return
	fi

	local head_sha porcelain
	head_sha="$(git -C "$target" rev-parse HEAD 2>/dev/null || true)"
	porcelain="$(git -C "$target" status --porcelain 2>/dev/null || true)"
	if [ -z "$porcelain" ]; then
		ST_DIRTY["$key"]="clean"
	else
		ST_DIRTY["$key"]="dirty ($(printf '%s\n' "$porcelain" | grep -c .) files)"
	fi
	ST_HEAD["$key"]="$head_sha"

	if [ -n "$head_sha" ] && [ "$head_sha" = "$pin" ]; then
		ST_STATUS["$key"]="ok"
		ST_AHEAD["$key"]=0
		ST_BEHIND["$key"]=0
		CNT_OK=$((CNT_OK + 1))
	elif [ -n "$head_sha" ] && git -C "$target" cat-file -e "${pin}^{commit}" 2>/dev/null; then
		ST_STATUS["$key"]="drift"
		local counts
		counts="$(git -C "$target" rev-list --left-right --count "${pin}...${head_sha}" 2>/dev/null || printf '0\t0')"
		ST_BEHIND["$key"]="$(printf '%s' "$counts" | cut -f1)"
		ST_AHEAD["$key"]="$(printf '%s' "$counts" | cut -f2)"
		CNT_DRIFT=$((CNT_DRIFT + 1))
	else
		ST_STATUS["$key"]="unfetched"
		CNT_UNFETCHED=$((CNT_UNFETCHED + 1))
	fi
}

print_text_report() {
	printf 'versions.lock.yaml: %s (%d pins)\n' "$REPO_NAME" "${#LF_KEYS[@]}"
	printf 'sibling root:       %s\n' "$S"
	printf '\n'

	if [ "$QUIET" -eq 0 ]; then
		printf '%-20s%-11s%-10s%-10s%-12s%s\n' REPO STATUS PINNED HEAD "vs PIN" WORKTREE
		local key status pinned8 head8 vspin worktree
		for key in "${SELECTED_KEYS[@]}"; do
			status="${ST_STATUS[$key]}"
			pinned8="${LF_SHA[$key]:0:8}"
			worktree="${ST_DIRTY[$key]}"
			case "$status" in
			ok)
				head8="${ST_HEAD[$key]:0:8}"
				vspin="="
				;;
			drift)
				head8="${ST_HEAD[$key]:0:8}"
				vspin="+${ST_AHEAD[$key]} / -${ST_BEHIND[$key]}"
				;;
			unfetched)
				head8="${ST_HEAD[$key]:0:8}"
				vspin="?"
				;;
			*)
				head8="-"
				vspin="-"
				worktree="-"
				;;
			esac
			printf '%-20s%-11s%-10s%-10s%-12s%s\n' "$key" "$status" "$pinned8" "$head8" "$vspin" "$worktree"
		done
		printf '\n'
	fi

	local -a parts=()
	[ "$CNT_OK" -gt 0 ] && parts+=("$CNT_OK ok")
	[ "$CNT_DRIFT" -gt 0 ] && parts+=("$CNT_DRIFT drift")
	[ "$CNT_UNFETCHED" -gt 0 ] && parts+=("$CNT_UNFETCHED unfetched")
	[ "$CNT_MISSING" -gt 0 ] && parts+=("$CNT_MISSING missing")
	[ "$CNT_UNPINNED" -gt 0 ] && parts+=("$CNT_UNPINNED unpinned")
	local summary=""
	if [ "${#parts[@]}" -gt 0 ]; then
		local p
		for p in "${parts[@]}"; do
			if [ -z "$summary" ]; then
				summary="$p"
			else
				summary="$summary, $p"
			fi
		done
	else
		summary="0 pins evaluated"
	fi
	printf '%s.\n' "$summary"

	printf 'Local checkouts deliberately float; pins govern CI and fresh clones.\n'
	# shellcheck disable=SC2016 # the backticks below are literal, not command substitution
	printf 'Run `make pins.update` to record the current origin/main SHAs, or\n'
	# shellcheck disable=SC2016 # ditto
	printf '`./scripts/checkout-deps.sh --pinned --into <dir>` for an isolated pinned tree.\n'
}

print_json_report() {
	local lockfile_abs
	lockfile_abs="$(cd "$(dirname "$LOCKFILE")" && pwd)/$(basename "$LOCKFILE")"

	printf '{\n'
	printf '  "lockfile": "%s",\n' "$(json_escape "$lockfile_abs")"
	printf '  "repo": "%s",\n' "$(json_escape "$REPO_NAME")"
	printf '  "siblingRoot": "%s",\n' "$(json_escape "$S")"
	printf '  "summary": { "ok": %d, "drift": %d, "unfetched": %d, "missing": %d, "unpinned": %d },\n' \
		"$CNT_OK" "$CNT_DRIFT" "$CNT_UNFETCHED" "$CNT_MISSING" "$CNT_UNPINNED"
	printf '  "pins": [\n'

	local n="${#SELECTED_KEYS[@]}" i=0 key status pinned8
	local head_json ahead_json behind_json dirty_json
	for key in "${SELECTED_KEYS[@]}"; do
		i=$((i + 1))
		status="${ST_STATUS[$key]}"
		pinned8="${LF_SHA[$key]:0:8}"
		head_json="null"
		ahead_json="null"
		behind_json="null"
		dirty_json="null"
		if [ "$status" != "missing" ]; then
			head_json="\"${ST_HEAD[$key]:0:8}\""
			case "$status" in
			ok)
				ahead_json="${ST_AHEAD[$key]}"
				behind_json="${ST_BEHIND[$key]}"
				;;
			drift)
				ahead_json="${ST_AHEAD[$key]}"
				behind_json="${ST_BEHIND[$key]}"
				;;
			esac
			case "${ST_DIRTY[$key]}" in
			clean) dirty_json="false" ;;
			dirty*) dirty_json="true" ;;
			esac
		fi
		printf '    { "repo": "%s", "status": "%s", "pinned": "%s", "head": %s,\n      "ahead": %s, "behind": %s, "dirty": %s }' \
			"$(json_escape "$key")" "$status" "$pinned8" "$head_json" "$ahead_json" "$behind_json" "$dirty_json"
		if [ "$i" -lt "$n" ]; then
			printf ',\n'
		else
			printf '\n'
		fi
	done

	printf '  ]\n'
	printf '}\n'
}

run_verify() {
	local key
	for key in "${SELECTED_KEYS[@]}"; do
		compute_status "$key"
		if [ "$REMOTE_CHECK" -eq 1 ]; then
			if check_remote_reachable "$key" "${LF_SHA[$key]}"; then
				REMOTE_OK["$key"]=1
			else
				REMOTE_OK["$key"]=0
			fi
		fi
	done

	if [ "$FORMAT" = "json" ]; then
		print_json_report
	else
		print_text_report
	fi

	if [ "$STRICT" -eq 1 ]; then
		local fail=0 status
		for key in "${SELECTED_KEYS[@]}"; do
			status="${ST_STATUS[$key]}"
			if [ "$REMOTE_CHECK" -eq 1 ]; then
				# --remote reframes the gate around remote resolvability, the
				# property architect correction (A) cares about, rather than
				# local disk state: a CI runner legitimately has no siblings
				# checked out yet, so `missing` alone must not fail it.
				if [ "$status" != "ok" ] && [ "$status" != "missing" ]; then
					fail=1
				fi
				if [ "${REMOTE_OK[$key]:-1}" -eq 0 ]; then
					fail=1
				fi
			else
				if [ "$status" != "ok" ]; then
					fail=1
				fi
			fi
		done
		if [ "$fail" -eq 1 ]; then
			exit 1
		fi
	fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

MODE=""
INTO_DIR=""
LOCKFILE=""
REPOS_FILTER=()
FORMAT="text"
STRICT=0
REMOTE_CHECK=0
DEPTH=1
SUBMODULES=0
QUIET=0

while [ $# -gt 0 ]; do
	case "$1" in
	--verify)
		if [ -n "$MODE" ] && [ "$MODE" != "verify" ]; then
			err "usage: --verify and --pinned are mutually exclusive"
			exit 2
		fi
		MODE="verify"
		shift
		;;
	--pinned)
		if [ -n "$MODE" ] && [ "$MODE" != "pinned" ]; then
			err "usage: --verify and --pinned are mutually exclusive"
			exit 2
		fi
		MODE="pinned"
		shift
		;;
	--into)
		[ $# -ge 2 ] || {
			err "--into requires a DIR argument"
			exit 2
		}
		INTO_DIR="$2"
		shift 2
		;;
	--lockfile)
		[ $# -ge 2 ] || {
			err "--lockfile requires a PATH argument"
			exit 2
		}
		LOCKFILE="$2"
		shift 2
		;;
	--repos)
		[ $# -ge 2 ] || {
			err "--repos requires a value, e.g. --repos \"mod-core mod-audit\""
			exit 2
		}
		IFS=' ' read -r -a REPOS_FILTER <<<"$2"
		shift 2
		;;
	--format)
		[ $# -ge 2 ] || {
			err "--format requires text|json"
			exit 2
		}
		case "$2" in
		text | json) FORMAT="$2" ;;
		*)
			err "--format must be 'text' or 'json', got: $2"
			exit 2
			;;
		esac
		shift 2
		;;
	--strict)
		STRICT=1
		shift
		;;
	--remote)
		REMOTE_CHECK=1
		shift
		;;
	--depth)
		[ $# -ge 2 ] || {
			err "--depth requires N"
			exit 2
		}
		case "$2" in
		'' | *[!0-9]*)
			err "--depth must be a non-negative integer, got: $2"
			exit 2
			;;
		esac
		DEPTH="$2"
		shift 2
		;;
	--submodules)
		SUBMODULES=1
		shift
		;;
	-q | --quiet)
		QUIET=1
		shift
		;;
	-h | --help)
		print_help
		exit 0
		;;
	*)
		err "unknown option: $1 (see --help)"
		exit 2
		;;
	esac
done

MODE="${MODE:-verify}"

# Determine token presence exactly once, without ever letting the token's
# value pass through xtrace: even `[ -n "$VAR" ]` echoes VAR's expanded
# value under `set -x`, so tracing must be off for this check regardless of
# the caller's own tracing state. Everything downstream branches on the
# boolean $TOKEN_SET, never on a direct `-n "${MODULEFORGE_READ_TOKEN:-}"`
# test, so no later `if` anywhere in this script can leak it either.
_was_xtrace=0
case "$-" in *x*) _was_xtrace=1 ;; esac
[ "$_was_xtrace" -eq 1 ] && set +x
if [ -n "${MODULEFORGE_READ_TOKEN:-}" ]; then TOKEN_SET=1; else TOKEN_SET=0; fi
[ "$_was_xtrace" -eq 1 ] && set -x
unset _was_xtrace

if [ "$DEPTH" = "0" ]; then
	DEPTH_ARGS=()
else
	DEPTH_ARGS=(--depth "$DEPTH")
fi

R="$(git rev-parse --path-format=absolute --show-toplevel 2>/dev/null)" || {
	err "not inside a git repo"
	exit 3
}
REPO_NAME="$(basename "$R")"

LOCKFILE="${LOCKFILE:-$R/versions.lock.yaml}"

declare -A LF_SHA=()
LF_KEYS=()
read_lockfile "$LOCKFILE"

if [ "${#REPOS_FILTER[@]}" -gt 0 ]; then
	SELECTED_KEYS=()
	for r in "${REPOS_FILTER[@]}"; do
		if [ -z "${LF_SHA[$r]+x}" ]; then
			err "unknown --repos key: '$r' (not in $LOCKFILE)"
			exit 2
		fi
		SELECTED_KEYS+=("$r")
	done
else
	SELECTED_KEYS=("${LF_KEYS[@]}")
fi

if [ -n "$INTO_DIR" ]; then
	S="$INTO_DIR"
elif [ -n "${MODULEFORGE_SIBLINGS_DIR:-}" ]; then
	S="${MODULEFORGE_SIBLINGS_DIR}"
else
	S="$(dirname "$R")"
fi
if [ -d "$S" ]; then
	S="$(cd "$S" && pwd)"
fi

declare -A ST_STATUS=() ST_HEAD=() ST_AHEAD=() ST_BEHIND=() ST_DIRTY=() REMOTE_OK=()

case "$MODE" in
verify) run_verify ;;
pinned) run_pinned ;;
esac

exit 0
