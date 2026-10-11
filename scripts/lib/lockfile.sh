# shellcheck shell=bash
# scripts/lib/lockfile.sh — the ONE versions.lock.yaml reader, sourced by
# scripts/checkout-deps.sh, scripts/update-pins.sh and scripts/link-siblings.sh
# so all three parse the lockfile identically.
#
# Sourced-only: defining functions is the only thing this file does at source
# time (no argument parsing, no exit, no trap). It is shared by copy, like the
# scripts that source it.
#
# The caller must define `err MESSAGE...` (prefixing and writing to stderr) before
# calling read_lockfile. Every failure below calls `err` and `exit 3` (the
# "malformed lockfile" precondition code). TOKEN_SET (0/1, default 0) says
# whether a read token is in use; only then is the token host allowlist applied.
#
# The lockfile is git-tracked and so untrusted input, and there is no YAML
# library: the schema is deliberately flat and line-oriented. Because a regex
# line-scan is not a YAML parser, anything that is not unambiguously the
# documented shape fails closed (exit 3, with the line number) instead of being
# silently skipped:
#   - a pin line that is not exactly `  <key>: <40-hex>` (+ optional `# comment`)
#     — quoted values, tab or odd indentation, longer-than-40-char SHA values;
#   - any indented content outside the `pins:` and `overrides:` blocks;
#   - a duplicate pin key, duplicate top-level host/owner/pins/overrides;
#   - more than one YAML document (`---` after content, a second `---`, `...`);
#   - a pin-shaped line inside `overrides:` (it has its own, stricter grammar).
#
# Populates globals: LF_HOST, LF_OWNER, LF_KEYS (ordered array), LF_SHA (map,
# SHAs lower-cased) and the per-key overrides maps LF_OV_HOST / LF_OV_OWNER /
# LF_OV_REPO.

# Shape checks for values read from the git-tracked lockfile. Each is the
# only gate between untrusted text and a clone URL / auth-header scope, so
# they are strict allowlist regexes (no '/', '@', ':', '?', '#', whitespace,
# or '..'), and callers report a fixed message that never echoes the value.
valid_host() { [[ "$1" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ ]] && [[ "$1" != *..* ]]; }
valid_owner() { [[ "$1" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$ ]]; }
# Repo names and pin keys may contain '.', '_' and '-', but never be '.' or
# '..' (path traversal in the URL and the checkout directory) or lead with '-'.
valid_repo() { [[ "$1" =~ ^[A-Za-z0-9_.-]+$ ]] && [[ "$1" != . && "$1" != .. && "$1" != -* ]]; }

# token_host_allowed HOST -- is HOST on the token allowlist
# (MODULEFORGE_TOKEN_ALLOWED_HOSTS, default github.com)? Hostnames compare
# case-insensitively. A malformed allowlist entry is a hard refusal (exit 3).
# Every entry is validated, not just the ones scanned before a match.
token_host_allowed() {
	local allowed="${MODULEFORGE_TOKEN_ALLOWED_HOSTS:-github.com}" h host_lc="${1,,}" found=1
	allowed="${allowed//,/ }"
	# Split on whitespace without pathname expansion: an entry such as `github.c*`
	# must stay a (malformed) literal, not expand against the cwd.
	local had_noglob=0
	case "$-" in *f*) had_noglob=1 ;; esac
	set -f
	for h in $allowed; do
		if ! valid_host "$h"; then
			err "MODULEFORGE_TOKEN_ALLOWED_HOSTS has an entry that is not a plain hostname"
			exit 3
		fi
		[ "${h,,}" = "$host_lc" ] && found=0
	done
	[ "$had_noglob" -eq 1 ] || set +f
	return "$found"
}

# require_token_host_allowed HOST -- with a token set, HOST must be on the
# allowlist, checked before any network call. Without a token there is
# nothing to protect and the SSH path is not restricted.
require_token_host_allowed() {
	[ "${TOKEN_SET:-0}" -eq 1 ] || return 0
	token_host_allowed "$1" && return 0
	err "refusing to send MODULEFORGE_READ_TOKEN: a lockfile host is not in the token host allowlist (default github.com; set MODULEFORGE_TOKEN_ALLOWED_HOSTS for a self-hosted forge)"
	exit 3
}

# shellcheck disable=SC2034 # LF_* globals are consumed by the sourcing scripts
read_lockfile() {
	local file="$1"
	if [ ! -f "$file" ]; then
		err "lockfile not found: $file"
		exit 3
	fi

	declare -gA LF_SHA=() LF_OV_HOST=() LF_OV_OWNER=() LF_OV_REPO=() LF_OV_SEEN=()
	LF_KEYS=()
	LF_HOST=""
	LF_OWNER=""

	# Sections are tracked by the top-level (zero-indent) key. Pin lines are only
	# valid inside `pins:`; the optional `overrides:` block (see
	# versions-lockfile.md, "The `overrides` block") has its own grammar:
	# entries are `  <key>:` at two spaces with fields at four.
	local section=none lineno=0 seen_content=0 seen_docstart=0
	local seen_pins=0 seen_ov=0 seen_host=0 seen_owner=0
	local cur_ov="" line stripped fname fval key sha
	local -a ov_keys=()
	local pin_re='^[ ]{2}([A-Za-z0-9._-]+):[[:space:]]+([0-9a-fA-F]{40})[[:space:]]*(#.*)?$'
	while IFS= read -r line || [ -n "$line" ]; do
		lineno=$((lineno + 1))
		line="${line%$'\r'}"

		# One document only: a leading `---` is fine, anything else is not.
		if [[ "$line" =~ ^(---|\.\.\.)([[:space:]]|$) ]]; then
			if [ "$seen_content" -eq 1 ] || [ "$seen_docstart" -eq 1 ] || [[ "$line" == ...* ]]; then
				err "malformed lockfile: line $lineno: multiple YAML documents are not supported: $file"
				exit 3
			fi
			seen_docstart=1
			continue
		fi

		# Top-level (zero-indent, non-comment, non-blank) line switches section.
		if [[ "$line" =~ ^[^[:space:]#] ]]; then
			seen_content=1
			cur_ov=""
			if [[ "$line" =~ ^pins:[[:space:]]*(#.*)?$ ]]; then
				[ "$seen_pins" -eq 0 ] || { err "malformed lockfile: line $lineno: duplicate pins: block: $file"; exit 3; }
				seen_pins=1
				section=pins
			elif [[ "$line" =~ ^overrides:[[:space:]]*(#.*)?$ ]]; then
				[ "$seen_ov" -eq 0 ] || { err "malformed lockfile: line $lineno: duplicate overrides: block: $file"; exit 3; }
				seen_ov=1
				section=overrides
			else
				section=other
				# Everything after the key, not the second ': '-separated field: a
				# value such as `evil:80` must reach the shape check whole, not be
				# truncated into something that passes it. Only the ends are
				# trimmed: whitespace inside a value is a shape failure.
				if [[ "$line" =~ ^(host|owner):[[:space:]]*(.*)$ ]]; then
					fname="${BASH_REMATCH[1]}"
					fval="${BASH_REMATCH[2]}"
					fval="${fval%"${fval##*[![:space:]]}"}"
					if [ "$fname" = host ]; then
						[ "$seen_host" -eq 0 ] || { err "malformed lockfile: line $lineno: duplicate host: key: $file"; exit 3; }
						seen_host=1
						LF_HOST="$fval"
					else
						[ "$seen_owner" -eq 0 ] || { err "malformed lockfile: line $lineno: duplicate owner: key: $file"; exit 3; }
						seen_owner=1
						LF_OWNER="$fval"
					fi
				fi
			fi
			continue
		fi

		# Blank and comment-only lines carry no content.
		[[ "$line" =~ ^[[:space:]]*(#.*)?$ ]] && continue
		seen_content=1

		case "$section" in
		pins)
			if [[ "$line" =~ $pin_re ]]; then
				key="${BASH_REMATCH[1]}"
				sha="${BASH_REMATCH[2]}"
				valid_repo "$key" || { err "malformed lockfile: line $lineno: a pin key is not a plain repository name: $file"; exit 3; }
				if [ -n "${LF_SHA[$key]+x}" ]; then
					err "malformed lockfile: line $lineno: duplicate pin key '$key': $file"
					exit 3
				fi
				LF_KEYS+=("$key")
				LF_SHA["$key"]="${sha,,}"
			else
				err "malformed lockfile: line $lineno: not a valid pin (expected exactly two spaces, then <repo>: <40-hex sha>, optional # comment; no quotes, tabs or longer values): $file"
				exit 3
			fi
			;;
		overrides)
			# Inside overrides: comment-only lines were skipped above; strip a
			# trailing comment and re-check for blank.
			stripped="${line%%#*}"
			[[ "$stripped" =~ ^[[:space:]]*$ ]] && continue
			if [[ "$stripped" =~ ^\ \ ([A-Za-z0-9._-]+):[[:space:]]*$ ]]; then
				cur_ov="${BASH_REMATCH[1]}"
				valid_repo "$cur_ov" || { err "malformed lockfile: an overrides entry key is not a plain repository name"; exit 3; }
				if [ -n "${LF_OV_SEEN[$cur_ov]+x}" ]; then
					err "malformed lockfile: duplicate overrides entry '$cur_ov': $file"
					exit 3
				fi
				LF_OV_SEEN["$cur_ov"]=1
				ov_keys+=("$cur_ov")
			elif [ -n "$cur_ov" ] && [[ "$stripped" =~ ^\ \ \ \ ([A-Za-z0-9_]+):[[:space:]]*(.*)$ ]]; then
				fname="${BASH_REMATCH[1]}"
				fval="${BASH_REMATCH[2]}"
				# trim trailing whitespace, then one layer of matching quotes
				fval="${fval%"${fval##*[![:space:]]}"}"
				if [[ "$fval" =~ ^\"(.*)\"$ ]] || [[ "$fval" =~ ^\'(.*)\'$ ]]; then
					fval="${BASH_REMATCH[1]}"
				fi
				case "$fname" in
				host)
					valid_host "$fval" || { err "malformed lockfile: overrides.$cur_ov.host is not a plain hostname: $file"; exit 3; }
					[ -z "${LF_OV_HOST[$cur_ov]+x}" ] || { err "malformed lockfile: line $lineno: duplicate field '$fname' in overrides.$cur_ov: $file"; exit 3; }
					LF_OV_HOST["$cur_ov"]="$fval"
					;;
				owner)
					valid_owner "$fval" || { err "malformed lockfile: overrides.$cur_ov.owner is not a plain account name: $file"; exit 3; }
					[ -z "${LF_OV_OWNER[$cur_ov]+x}" ] || { err "malformed lockfile: line $lineno: duplicate field '$fname' in overrides.$cur_ov: $file"; exit 3; }
					LF_OV_OWNER["$cur_ov"]="$fval"
					;;
				repo)
					valid_repo "$fval" || { err "malformed lockfile: overrides.$cur_ov.repo is not a plain repository name: $file"; exit 3; }
					[ -z "${LF_OV_REPO[$cur_ov]+x}" ] || { err "malformed lockfile: line $lineno: duplicate field '$fname' in overrides.$cur_ov: $file"; exit 3; }
					LF_OV_REPO["$cur_ov"]="$fval"
					;;
				*)
					err "malformed lockfile: unknown field '$fname' in overrides.$cur_ov (only host, owner, repo are accepted): $file"
					exit 3
					;;
				esac
			else
				# A pin-shaped line (`  mod-core: <sha>`) lands here too.
				err "malformed lockfile: line $lineno: unrecognized line in overrides block: $stripped ($file)"
				exit 3
			fi
			;;
		*)
			err "malformed lockfile: line $lineno: indented content outside the pins: and overrides: blocks: $file"
			exit 3
			;;
		esac
	done <"$file"

	if [ -z "$LF_HOST" ] || [ -z "$LF_OWNER" ]; then
		err "malformed lockfile: missing host/owner: $file"
		exit 3
	fi
	# The lockfile is git-tracked, so host and owner are untrusted: they become
	# a clone URL and, with a token, the scope of an auth header. Refuse a wrong
	# shape with a fixed message that names the key and never echoes the value.
	if ! valid_host "$LF_HOST"; then
		err "malformed lockfile: host: is not a plain hostname"
		exit 3
	fi
	if ! valid_owner "$LF_OWNER"; then
		err "malformed lockfile: owner: is not a plain account name"
		exit 3
	fi

	local ok
	for ok in ${ov_keys[@]+"${ov_keys[@]}"}; do
		if [ -z "${LF_SHA[$ok]+x}" ]; then
			err "malformed lockfile: overrides.$ok has no matching key under pins: $file"
			exit 3
		fi
	done

	if [ "${#LF_KEYS[@]}" -eq 0 ]; then
		err "malformed lockfile: no pins found: $file"
		exit 3
	fi

	# With a token, every host that can receive it must be allowlisted, checked
	# here before any network call: the top-level host and each override host.
	local k
	for k in "${LF_KEYS[@]}"; do
		require_token_host_allowed "${LF_OV_HOST[$k]:-$LF_HOST}"
	done
}
