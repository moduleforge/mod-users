#!/usr/bin/env bash
# scripts/test-checkout-deps.sh — tests for the lockfile and token guards in
# scripts/checkout-deps.sh: a hostile `host:` / `owner:` (top-level or in an
# `overrides` entry) in the git-tracked versions.lock.yaml must never receive
# the read token, the auth header must be scoped to the fetch target's host,
# redirects must be off, and the token must never appear on a command line
# (checked against the live process table at fetch time), and must never appear
# in the output of a run traced with `bash -x` or SHELLOPTS=xtrace.
#
# Known limit: the ps/argv check is a single snapshot of the process table
# taken once, inside the stub, at fetch time. It cannot see a token that is on a
# command line only before or after that instant, or in a process that has
# already exited.
#
# A stub `git` first on PATH records every `fetch` (its argv, the GIT_CONFIG_*
# environment, and a snapshot of every process's command line) and answers it
# with success, so no network is touched; every other git command runs the real
# git against a throwaway repository. The token is a random, obviously fake
# value generated per run and is never printed.
#
# Run: bash scripts/test-checkout-deps.sh   (wired into `make test.unit.scripts`)
# Exit: 0 all passed; 1 a case failed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUT="$SCRIPT_DIR/checkout-deps.sh"
LIB="$SCRIPT_DIR/lib/lockfile.sh"
UPDATE_PINS="$SCRIPT_DIR/update-pins.sh"
LINK_SIBLINGS="$SCRIPT_DIR/link-siblings.sh"
REAL_GIT="$(command -v git)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/test-checkout-deps.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

# A random, obviously fake token per run: never a real credential.
TOKEN="FAKE_tok_$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"
TOKEN_B64="$(printf 'x-access-token:%s' "$TOKEN" | base64 | tr -d '\n')"

mkdir -p "$WORK/bin" "$WORK/repo" "$WORK/siblings"
cat >"$WORK/bin/git" <<'STUB'
#!/usr/bin/env bash
# The stub compares the auth header, so it must not trace itself even when a
# test exports SHELLOPTS=xtrace (the trace is the subject under test only for
# the script under test, not for this harness).
set +x
# STUB_REAL=1: pass everything through to the real git (tests that need a real
# fetch from a local fake remote). Otherwise fetch is faked and merge-base
# answers STUB_MB_RC (default 0), since the faked fetch fetches no objects.
[ "${STUB_REAL:-0}" = 1 ] && exec "$REAL_GIT" "$@"
for a in "$@"; do [ "$a" = merge-base ] && exit "${STUB_MB_RC:-0}"; done
is_fetch=0
for a in "$@"; do [ "$a" = fetch ] && is_fetch=1; done
if [ "$is_fetch" -eq 1 ]; then
	hdr_ok=0
	[ "${GIT_CONFIG_VALUE_0:-}" = "AUTHORIZATION: basic ${EXPECT_B64:-unset}" ] && hdr_ok=1
	{
		printf 'FETCH argv=%s\n' "$*"
		printf 'FETCH count=%s key0=%s key1=%s val1=%s hdr_ok=%s\n' "${GIT_CONFIG_COUNT:-}" "${GIT_CONFIG_KEY_0:-}" "${GIT_CONFIG_KEY_1:-}" "${GIT_CONFIG_VALUE_1:-}" "$hdr_ok"
	} >>"$STUB_LOG"
	# Every process's full command line, as ps (and /proc/<pid>/cmdline) shows it.
	ps -ww -eo args= >>"$STUB_PS" 2>/dev/null || true
	exit 0
fi
exec "$REAL_GIT" "$@"
STUB
chmod +x "$WORK/bin/git"
"$REAL_GIT" init --quiet "$WORK/repo"

FAILS=0
pass() { printf 'PASS  %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; FAILS=$((FAILS + 1)); }

# mklock HOST OWNER [OVERRIDES_BLOCK] -> writes the lockfile, prints its path.
mklock() {
	local f="$WORK/lock.yaml"
	printf 'lockfileVersion: 1\nhost:  %s\nowner: %s\n\npins:\n  mod-core:  0123456789abcdef0123456789abcdef01234567\n' "$1" "$2" >"$f"
	[ -n "${3:-}" ] && printf '\noverrides:\n%s\n' "$3" >>"$f"
	printf '%s' "$f"
}

# run_sut TOKEN_MODE LOCKFILE ARGS... — sets RC, OUT (stdout+stderr), LOG (stub
# fetch log) and PSLOG (process table snapshots at fetch time). TOKEN_MODE is
# `token` or `notoken`. Extra environment comes from EXTRA_ENV (NAME=value).
EXTRA_ENV=()
SUT_FLAGS=() # extra bash flags for the script under test (e.g. -x)
run_sut() {
	local mode="$1" lock="$2"
	shift 2
	: >"$WORK/stub.log"
	: >"$WORK/stub.ps"
	local -a envs=(PATH="$WORK/bin:$PATH" REAL_GIT="$REAL_GIT" STUB_LOG="$WORK/stub.log" STUB_PS="$WORK/stub.ps" EXPECT_B64="$TOKEN_B64")
	[ "$mode" = token ] && envs+=(MODULEFORGE_READ_TOKEN="$TOKEN")
	[ "${#EXTRA_ENV[@]}" -gt 0 ] && envs+=("${EXTRA_ENV[@]}")
	RC=0
	OUT="$(cd "$WORK/repo" && env -u MODULEFORGE_READ_TOKEN -u MODULEFORGE_TOKEN_ALLOWED_HOSTS "${envs[@]}" \
		bash ${SUT_FLAGS[@]+"${SUT_FLAGS[@]}"} "$SUT" --lockfile "$lock" --into "$WORK/siblings" "$@" 2>&1)" || RC=$?
	LOG="$(cat "$WORK/stub.log")"
	PSLOG="$(cat "$WORK/stub.ps")"
	# LOGP is the only form of the log that failure messages print: the fake
	# token and its base64 are redacted so even a failing run never emits them.
	LOGP="${LOG//"$TOKEN_B64"/<redacted>}"
	LOGP="${LOGP//"$TOKEN"/<redacted>}"
}

no_fetch() { [ -z "$LOG" ]; }
no_leak() { # the token and its base64 form appear nowhere in the output, argv log or process table
	case "$OUT$LOG$PSLOG" in *"$TOKEN"* | *"$TOKEN_B64"*) return 1 ;; esac
	return 0
}

# --- hostile or non-allowlisted host with a token: refused before any fetch ---
for host in evil.example 'host@evil' 'host/x' 'evil:80' 'host?x' 'evil.example#' 'a b' 'a..b' '-lead.example' 'trail-' 'user:pw@github.com'; do
	for mode in "--verify --remote" "--pinned"; do
		# shellcheck disable=SC2086 # the mode is a word list
		run_sut token "$(mklock "$host" moduleforge)" $mode
		if [ "$RC" -eq 3 ] && no_fetch && no_leak; then
			pass "token + host '$host' ($mode): exit 3, no fetch, no token output"
		else
			fail "token + host '$host' ($mode): rc=$RC log='$LOGP'"
		fi
	done
done

# a hostile value is not echoed back
run_sut token "$(mklock 'host@evil-credentials-here' moduleforge)" --verify --remote
case "$OUT" in *evil-credentials-here*) fail "the hostile host value is echoed" ;; *) pass "a malformed host is refused without echoing it" ;; esac

# --- hostile owner ----------------------------------------------------------
for owner in 'evil/x' '../x' '..' '.' 'a b' 'ow@ner' '-lead' 'trail-' 'a:b' 'a%2fb'; do
	run_sut token "$(mklock github.com "$owner")" --verify --remote
	if [ "$RC" -eq 3 ] && no_fetch && no_leak; then
		pass "token + owner '$owner': exit 3, no fetch"
	else
		fail "token + owner '$owner': rc=$RC log='$LOGP'"
	fi
done

# --- shape is validated on the SSH (no token) path too ---------------------
for host in 'host@evil' 'host/x' 'evil:80'; do
	run_sut notoken "$(mklock "$host" moduleforge)" --verify --remote
	if [ "$RC" -eq 3 ] && no_fetch; then
		pass "no token + host '$host': still refused by shape"
	else
		fail "no token + host '$host': rc=$RC log='$LOGP'"
	fi
done
run_sut notoken "$(mklock github.com 'evil/x')" --verify --remote
if [ "$RC" -eq 3 ] && no_fetch; then pass "no token + hostile owner: refused by shape"; else fail "no token + hostile owner: rc=$RC"; fi

# --- overrides: same shape rules, and the allowlist covers override hosts ---
for ov in \
	'  mod-core:
    host: evil.example' \
	'  mod-core:
    host: "a@b"' \
	'  mod-core:
    owner: ..' \
	'  mod-core:
    owner: "a/b"' \
	'  mod-core:
    repo: ..' \
	'  mod-core:
    repo: "x/y"' \
	'  mod-core:
    repo: -rf'; do
	run_sut token "$(mklock github.com moduleforge "$ov")" --verify --remote
	if [ "$RC" -eq 3 ] && no_fetch && no_leak; then
		pass "token + hostile override ($(printf '%s' "$ov" | tr -s ' \n' ' ')): exit 3, no fetch"
	else
		fail "token + hostile override ($(printf '%s' "$ov" | tr -s ' \n' ' ')): rc=$RC log='$LOGP'"
	fi
done

# a pin key that would traverse out of the sibling root / URL path
printf 'lockfileVersion: 1\nhost: github.com\nowner: moduleforge\n\npins:\n  ..:  0123456789abcdef0123456789abcdef01234567\n' >"$WORK/dotdot.yaml"
run_sut token "$WORK/dotdot.yaml" --verify --remote
if [ "$RC" -eq 3 ] && no_fetch; then pass "a '..' pin key is refused"; else fail "'..' pin key: rc=$RC log='$LOGP'"; fi

# --- the SSH path is not subject to the allowlist, and sends no header ------
run_sut notoken "$(mklock git.example.com moduleforge)" --verify --remote
if [ "$RC" -eq 0 ] && grep -q 'git@git.example.com:moduleforge/mod-core.git' <<<"$LOG" && grep -q 'count= key0= key1=' <<<"$LOG"; then
	pass "no token + a non-allowlisted but well-formed host: SSH fetch, no auth config"
else
	fail "no token + git.example.com: rc=$RC log='$LOGP'"
fi

# --- the header key is scoped to the host, token only in environment --------
run_sut token "$(mklock github.com moduleforge)" --verify --remote
if [ "$RC" -eq 0 ] && grep -q 'count=2 key0=http.https://github.com/.extraheader key1=http.followRedirects val1=false hdr_ok=1' <<<"$LOG"; then
	pass "the auth header (carrying the token) is scoped to http.https://github.com/.extraheader and redirects are off"
else
	fail "scoped key: rc=$RC log='$LOGP'"
fi
if grep -q 'https://github.com/moduleforge/mod-core.git' <<<"$LOG"; then
	pass "the fetch target is on the scoped host"
else
	fail "fetch target: log='$LOGP'"
fi
if no_leak && ! grep -q 'extraheader=' <<<"$LOG"; then
	pass "no token (or its base64) in the fetch argv, and no inline extraheader config"
else
	fail "token leaked to argv"
fi

# --- the token is not in any process's argv (ps / /proc/<pid>/cmdline) ------
# Positive control first: the snapshot must show the script's own command line,
# otherwise an empty/failed `ps` would make the absence check vacuous.
if grep -q 'checkout-deps.sh --lockfile' <<<"$PSLOG"; then
	pass "process-table snapshot at fetch time contains the script's command line"
else
	fail "process-table snapshot is empty or incomplete; the argv check below proves nothing"
fi
case "$PSLOG" in
*"$TOKEN"* | *"$TOKEN_B64"*) fail "the token (or its base64) is visible in the process table" ;;
*) pass "the token and its base64 form are absent from every process's argv at fetch time" ;;
esac
# --pinned goes through the same fetch path (git init + remote add + fetch).
run_sut token "$(mklock github.com moduleforge)" --pinned
if no_leak && ! grep -q 'extraheader=' <<<"$LOG" && grep -q 'hdr_ok=1' <<<"$LOG"; then
	pass "--pinned: header via environment only, token absent from argv and output"
else
	fail "--pinned token handling: rc=$RC log='$LOGP'"
fi
rm -rf "${WORK:?}/siblings" && mkdir -p "$WORK/siblings"

# --- the allowlist override -------------------------------------------------
run_sut token "$(mklock git.example.com moduleforge)" --verify --remote
if [ "$RC" -eq 3 ] && no_fetch; then pass "git.example.com with a token is refused by default"; else fail "default allowlist: rc=$RC log='$LOGP'"; fi

EXTRA_ENV=(MODULEFORGE_TOKEN_ALLOWED_HOSTS="other.example, GIT.example.com")
run_sut token "$(mklock git.example.com moduleforge)" --verify --remote
if [ "$RC" -eq 0 ] && grep -q 'key0=http.https://git.example.com/.extraheader' <<<"$LOG" && no_leak; then
	pass "MODULEFORGE_TOKEN_ALLOWED_HOSTS admits a self-hosted forge, header scoped to it"
else
	fail "allowlist override: rc=$RC log='$LOGP'"
fi
EXTRA_ENV=(MODULEFORGE_TOKEN_ALLOWED_HOSTS="github.com evil@x")
run_sut token "$(mklock github.com moduleforge)" --verify --remote
if [ "$RC" -eq 3 ] && no_fetch; then pass "a malformed allowlist entry is refused"; else fail "malformed allowlist: rc=$RC log='$LOGP'"; fi

# An override host must itself be allowlisted, and the header is scoped to it.
OV_OK='  mod-core:
    host: ghe.example.com'
EXTRA_ENV=()
run_sut token "$(mklock github.com moduleforge "$OV_OK")" --verify --remote
if [ "$RC" -eq 3 ] && no_fetch; then pass "an override host outside the allowlist is refused with a token"; else fail "override host default allowlist: rc=$RC log='$LOGP'"; fi
EXTRA_ENV=(MODULEFORGE_TOKEN_ALLOWED_HOSTS="github.com ghe.example.com")
run_sut token "$(mklock github.com moduleforge "$OV_OK")" --verify --remote
if [ "$RC" -eq 0 ] && grep -q 'key0=http.https://ghe.example.com/.extraheader' <<<"$LOG" &&
	grep -q 'https://ghe.example.com/moduleforge/mod-core.git' <<<"$LOG" && no_leak; then
	pass "an allowlisted override host gets the header scoped to that host, not the top-level host"
else
	fail "override host scoping: rc=$RC log='$LOGP'"
fi
EXTRA_ENV=()

# --- the token never reaches a traced run -----------------------------------
# Under `bash -x` and under SHELLOPTS=xtrace (which also traces every bash child
# and any function the script defines), the `set +x` guards around each token
# dereference must keep the token and its base64 form out of stdout+stderr.
# Each case first proves the trace is live (otherwise absence proves nothing)
# and that the authenticated fetch actually ran (hdr_ok=1), so the guarded
# lines were exercised.
for trace in "bash -x" "SHELLOPTS=xtrace"; do
	for mode in "--verify --remote" "--pinned"; do
		SUT_FLAGS=()
		EXTRA_ENV=()
		if [ "$trace" = "bash -x" ]; then SUT_FLAGS=(-x); else EXTRA_ENV=(SHELLOPTS=xtrace); fi
		# shellcheck disable=SC2086 # the mode is a word list
		run_sut token "$(mklock github.com moduleforge)" $mode
		SUT_FLAGS=()
		EXTRA_ENV=()
		if ! grep -q '^+ ' <<<"$OUT"; then
			fail "$trace ($mode): no xtrace output was produced, so the leak check is vacuous"
		elif { [ "$RC" -eq 0 ] || [ "$mode" = "--pinned" ]; } && grep -q 'hdr_ok=1' <<<"$LOG" && no_leak; then
			# --pinned may exit 3 here: the stub fetch yields no FETCH_HEAD, so the
			# post-checkout HEAD==pin assertion (rightly) fails the clone.
			pass "$trace ($mode): authenticated fetch ran, token and base64 absent from traced output"
		else
			fail "$trace ($mode): rc=$RC log='$LOGP'"
		fi
		rm -rf "${WORK:?}/siblings" && mkdir -p "$WORK/siblings"
	done
done

# --- host edge cases --------------------------------------------------------
# A trailing-dot host (an FQDN spelling) fails the host shape check.
run_sut token "$(mklock 'github.com.' moduleforge)" --verify --remote
if [ "$RC" -eq 3 ] && no_fetch && no_leak; then pass "token + trailing-dot host 'github.com.': exit 3, no fetch"; else fail "trailing-dot host: rc=$RC log='$LOGP'"; fi

# A well-formed lookalike that merely starts with an allowlisted name is not on the allowlist.
run_sut token "$(mklock 'github.com.evil.example' moduleforge)" --verify --remote
if [ "$RC" -eq 3 ] && no_fetch && no_leak; then pass "token + lookalike host 'github.com.evil.example': exit 3, no fetch"; else fail "lookalike host: rc=$RC log='$LOGP'"; fi

# A CRLF-terminated lockfile (e.g. checked out with autocrlf) must still parse
# and scope the header to the clean host.
CRLF="$WORK/crlf.yaml"
printf 'lockfileVersion: 1\r\nhost:  github.com\r\nowner: moduleforge\r\n\r\npins:\r\n  mod-core:  0123456789abcdef0123456789abcdef01234567\r\n' >"$CRLF"
run_sut token "$CRLF" --verify --remote
if [ "$RC" -eq 0 ] && grep -q 'key0=http.https://github.com/.extraheader' <<<"$LOG" && grep -q 'hdr_ok=1' <<<"$LOG" && no_leak; then
	pass "a CRLF-terminated lockfile parses; header scoped to github.com"
else
	fail "CRLF lockfile: rc=$RC log='$LOGP'"
fi

# Hostnames compare case-insensitively: positive case with the default allowlist.
run_sut token "$(mklock 'GitHub.com' moduleforge)" --verify --remote
if [ "$RC" -eq 0 ] && grep -qi 'key0=http.https://github.com/.extraheader' <<<"$LOG" && grep -q 'hdr_ok=1' <<<"$LOG" && no_leak; then
	pass "token + lockfile host 'GitHub.com' is admitted by the default allowlist"
else
	fail "mixed-case host: rc=$RC log='$LOGP'"
fi

# --- the allowlist list is split without globbing ---------------------------
# A file named github.com in the cwd would make an unquoted `github.c*` expand
# to an allowlisted-looking name. With globbing off the entry is simply
# malformed (exit 3); with globbing on it would silently admit github.com.
: >"$WORK/repo/github.com"
EXTRA_ENV=(MODULEFORGE_TOKEN_ALLOWED_HOSTS="github.c*")
run_sut token "$(mklock github.com moduleforge)" --verify --remote
if [ "$RC" -eq 3 ] && no_fetch && no_leak; then pass "a glob in MODULEFORGE_TOKEN_ALLOWED_HOSTS is not expanded against the cwd"; else fail "allowlist glob: rc=$RC log='$LOGP'"; fi
EXTRA_ENV=()
rm -f "$WORK/repo/github.com"

# --- the allowlist re-check inside git_fetch_auth ---------------------------
# Unreachable from the CLI: read_lockfile already refuses any effective host
# that is not allowlisted, and clone_host returns that same value, so no
# lockfile can reach git_fetch_auth with a vetted-then-different host. The
# closest reachable path is to source the script's own function definitions
# (extracted verbatim, so this tests the shipped code, not a copy) and call
# git_fetch_auth directly with a non-allowlisted host.
FN_FILE="$WORK/fns.sh"
for fn in err valid_host token_host_allowed require_token_host_allowed git_fetch_auth; do
	awk -v fn="$fn" '
		!on && $0 ~ "^" fn "\\(\\) \\{" { on = 1; print; if ($0 ~ /\}[[:space:]]*$/ && $0 !~ /\{[[:space:]]*$/) on = 0; next }
		on { print; if ($0 ~ /^\}/) on = 0 }
	' "$SUT" "$LIB"
done >"$FN_FILE"
# direct_fetch HOST -> sets RC, OUT, LOG, PSLOG for git_fetch_auth with a token.
direct_fetch() {
	: >"$WORK/stub.log"
	: >"$WORK/stub.ps"
	RC=0
	# shellcheck disable=SC2016 # the bash -c body is single-quoted on purpose
	OUT="$(cd "$WORK/repo" && env -u MODULEFORGE_TOKEN_ALLOWED_HOSTS PATH="$WORK/bin:$PATH" REAL_GIT="$REAL_GIT" \
		STUB_LOG="$WORK/stub.log" STUB_PS="$WORK/stub.ps" EXPECT_B64="$TOKEN_B64" MODULEFORGE_READ_TOKEN="$TOKEN" FN_FILE="$FN_FILE" \
		bash -c 'set -euo pipefail; . "$FN_FILE"; TOKEN_SET=1; DEPTH_ARGS=(); git_fetch_auth "$1" origin deadbeef "$2"' _ "$WORK/repo" "$1" 2>&1)" || RC=$?
	LOG="$(cat "$WORK/stub.log")"
	PSLOG="$(cat "$WORK/stub.ps")"
}
# Positive control: an allowlisted host reaches the stub fetch via the same harness.
direct_fetch github.com
if [ "$RC" -eq 0 ] && grep -q 'hdr_ok=1' <<<"$LOG" && no_leak; then
	pass "git_fetch_auth (direct): an allowlisted host reaches the fetch (harness control)"
else
	fail "git_fetch_auth direct control: rc=$RC"
fi
direct_fetch evil.example
if [ "$RC" -eq 3 ] && no_fetch && no_leak; then
	pass "git_fetch_auth (direct): a non-allowlisted host is refused by the re-check, no fetch"
else
	fail "git_fetch_auth re-check: rc=$RC log='$LOGP'"
fi

# ===========================================================================
# Pin-script hardening (shared lockfile reader, --remote default-branch
# ancestry, strict grammar, update-pins literal keys + origin check, HEAD==pin,
# --strict dirty). Everything below runs in temp dirs under $WORK; no real
# lockfile is ever written.
# ===========================================================================
SHA=0123456789abcdef0123456789abcdef01234567
SHA2=89abcdef0123456789abcdef0123456789abcdef
G() { "$REAL_GIT" -c user.name=t -c user.email=t@t -c commit.gpgsign=false -c init.defaultBranch=main "$@"; }
rawlock() { # rawlock PRINTF_FORMAT_BODY -> lockfile with the standard header + body
	local f="$WORK/raw.yaml"
	# shellcheck disable=SC2059 # the body is a printf format on purpose
	{ printf 'lockfileVersion: 1\nhost: github.com\nowner: moduleforge\n\npins:\n'; printf "$1"; } >"$f"
	printf '%s' "$f"
}

# run_update_pins LOCKFILE ARGS... -> RC, OUT, LOG (stub fetch log). Sibling root $WORK/usib.
run_update_pins() {
	local lock="$1"
	shift
	: >"$WORK/stub.log"
	RC=0
	OUT="$(cd "$WORK/repo" && env PATH="$WORK/bin:$PATH" REAL_GIT="$REAL_GIT" STUB_LOG="$WORK/stub.log" STUB_PS="$WORK/stub.ps" \
		MODULEFORGE_SIBLINGS_DIR="$WORK/usib" bash "$UPDATE_PINS" --lockfile "$lock" "$@" 2>&1)" || RC=$?
	LOG="$(cat "$WORK/stub.log")"
}
# run_link_siblings LOCKFILE -> RC, OUT. Runs in a throwaway repo whose root holds the lockfile.
run_link_siblings() {
	rm -rf "${WORK:?}/lrepo" "${WORK:?}/agg"
	mkdir -p "$WORK/lrepo" "$WORK/agg/mod-core" "$WORK/agg/mod-authz"
	"$REAL_GIT" init --quiet "$WORK/lrepo"
	cp "$1" "$WORK/lrepo/versions.lock.yaml"
	RC=0
	OUT="$(cd "$WORK/lrepo" && env MODULEFORGE_SIBLINGS_DIR="$WORK/agg" bash "$LINK_SIBLINGS" 2>&1)" || RC=$?
	rm -f "$WORK/mod-core" "$WORK/mod-authz"
}

# --- strict fail-closed grammar: identical in all three scripts (F2/F3/F5) ---
# name|body (printf format); every one must exit 3, with a line number, in
# checkout-deps.sh, update-pins.sh and link-siblings.sh.
while IFS='|' read -r name body; do
	lock="$(rawlock "$body")"
	run_sut notoken "$lock" --verify
	if [ "$RC" -eq 3 ] && grep -q 'line [0-9]' <<<"$OUT" && no_fetch; then pass "checkout-deps: $name: exit 3 with a line number"; else fail "checkout-deps: $name: rc=$RC out='$OUT'"; fi
	run_update_pins "$lock" --dry-run
	if [ "$RC" -eq 3 ] && grep -q 'line [0-9]' <<<"$OUT"; then pass "update-pins: $name: exit 3 with a line number"; else fail "update-pins: $name: rc=$RC out='$OUT'"; fi
	run_link_siblings "$lock"
	if [ "$RC" -eq 3 ] && grep -q 'line [0-9]' <<<"$OUT"; then pass "link-siblings: $name: exit 3 with a line number"; else fail "link-siblings: $name: rc=$RC out='$OUT'"; fi
done <<EOF
double-quoted value|  mod-core: "$SHA"\n
single-quoted value|  mod-core: '$SHA'\n
tab indent|\tmod-core: $SHA\n
three-space indent|   mod-core: $SHA\n
one-space indent|  mod-core: $SHA\n mod-authz: $SHA\n
over-long (41+) sha value|  mod-core: ${SHA}0123\n
short sha value|  mod-core: ${SHA:0:39}\n
duplicate pin key|  mod-core: $SHA\n  mod-core: $SHA2\n
second yaml document|  mod-core: $SHA\n---\nhost: evil.example\npins:\n  mod-core: $SHA2\n
document end marker|  mod-core: $SHA\n...\n
pin line inside overrides|  mod-core: $SHA\n\noverrides:\n  mod-core: $SHA2\n
pin line in a block scalar|  mod-core: $SHA\nnotes: |\n  mod-authz: $SHA2\n
duplicate top-level host|  mod-core: $SHA\nhost: github.com\n
duplicate pins block|  mod-core: $SHA\npins:\n  mod-authz: $SHA2\n
EOF

# controls: a leading document marker, uppercase hex and a missing final newline are fine
run_sut notoken "$(rawlock "  mod-core: $SHA\n")" --verify
if [ "$RC" -eq 0 ]; then pass "control: a well-formed lockfile parses"; else fail "control lockfile: rc=$RC out='$OUT'"; fi
{ printf -- '---\n'; cat "$WORK/raw.yaml"; } >"$WORK/leading.yaml"
run_sut notoken "$WORK/leading.yaml" --verify
if [ "$RC" -eq 0 ]; then pass "a single leading --- document marker is accepted"; else fail "leading ---: rc=$RC out='$OUT'"; fi

# F2: a final pin line with no trailing newline is seen by all three scripts
lock="$(rawlock "  mod-core: $SHA\n  mod-authz: $SHA2")"
run_sut notoken "$lock" --verify
if [ "$RC" -eq 0 ] && grep -q '(2 pins)' <<<"$OUT"; then pass "checkout-deps sees a final pin with no trailing newline"; else fail "no-newline checkout-deps: rc=$RC out='$OUT'"; fi
run_update_pins "$lock" mod-authz
if [ "$RC" -eq 3 ] && grep -q 'sibling checkout not found' <<<"$OUT"; then pass "update-pins sees a final pin with no trailing newline"; else fail "no-newline update-pins: rc=$RC out='$OUT'"; fi
run_link_siblings "$lock"
if [ "$RC" -eq 0 ] && grep -q 'mod-authz' <<<"$OUT"; then pass "link-siblings sees a final pin with no trailing newline"; else fail "no-newline link-siblings: rc=$RC out='$OUT'"; fi
rm -f "$WORK/mod-core" "$WORK/mod-authz"

# --- F1: --remote requires the pin to be an ancestor of the default branch ---
RW="$WORK/remote-work"
G init -q "$RW"
echo a >"$RW/f"
G -C "$RW" add f
G -C "$RW" commit -qm main1
MAIN_SHA="$(G -C "$RW" rev-parse HEAD)"
G -C "$RW" switch -qc side
echo b >>"$RW/f"
G -C "$RW" commit -qam side-only
SIDE_SHA="$(G -C "$RW" rev-parse HEAD)"
G -C "$RW" switch -q main
G clone -q --bare "$RW" "$WORK/remote.git"
G -C "$WORK/remote.git" config uploadpack.allowAnySHA1InWant true
# a second remote whose HEAD names a branch that does not exist
cp -R "$WORK/remote.git" "$WORK/remote-nohead.git"
G -C "$WORK/remote-nohead.git" symbolic-ref HEAD refs/heads/gone
{
	printf '[url "%s"]\n\tinsteadOf = git@github.com:moduleforge/mod-core.git\n' "$WORK/remote.git"
	printf '[url "%s"]\n\tinsteadOf = git@github.com:moduleforge/mod-audit.git\n' "$WORK/remote-nohead.git"
} >"$WORK/gitconfig"
rm -rf "${WORK:?}/siblings" && mkdir -p "$WORK/siblings"
EXTRA_ENV=(STUB_REAL=1 GIT_CONFIG_GLOBAL="$WORK/gitconfig")

run_sut notoken "$(rawlock "  mod-core: $MAIN_SHA\n")" --verify --remote --strict
if [ "$RC" -eq 0 ]; then pass "--remote --strict: a pin on the default branch passes (real fetch, local fake remote)"; else fail "--remote main pin: rc=$RC out='$OUT'"; fi
run_sut notoken "$(rawlock "  mod-core: $SIDE_SHA\n")" --verify --remote --strict
if [ "$RC" -eq 1 ] && grep -q 'not an ancestor of the remote default branch' <<<"$OUT"; then pass "--remote --strict: a side-branch-only SHA is refused"; else fail "--remote side pin: rc=$RC out='$OUT'"; fi
run_sut notoken "$(rawlock "  mod-core: $SIDE_SHA\n")" --verify --remote
if [ "$RC" -eq 0 ] && grep -q 'not an ancestor' <<<"$OUT"; then pass "--remote without --strict reports the side-branch pin but keeps exit 0"; else fail "--remote non-strict side pin: rc=$RC out='$OUT'"; fi
run_sut notoken "$(rawlock "  mod-core: ${SHA}\n")" --verify --remote --strict
if [ "$RC" -eq 1 ] && grep -q 'cannot be fetched' <<<"$OUT"; then pass "--remote --strict: an unfetchable SHA is still refused"; else fail "--remote unfetchable: rc=$RC out='$OUT'"; fi
run_sut notoken "$(rawlock "  mod-audit: $MAIN_SHA\n")" --verify --remote --strict
if [ "$RC" -eq 1 ] && grep -q 'cannot determine the remote default branch' <<<"$OUT"; then pass "--remote --strict fails closed when the default branch cannot be determined"; else fail "--remote no default branch: rc=$RC out='$OUT'"; fi

# --- F8: --pinned asserts HEAD == pin after checkout -------------------------
run_sut notoken "$(rawlock "  mod-core: $MAIN_SHA\n")" --pinned
if [ "$RC" -eq 0 ] && [ "$(G -C "$WORK/siblings/mod-core" rev-parse HEAD)" = "$MAIN_SHA" ]; then pass "--pinned: real clone ends with HEAD == pin"; else fail "--pinned real clone: rc=$RC out='$OUT'"; fi
rm -rf "${WORK:?}/siblings" && mkdir -p "$WORK/siblings"
EXTRA_ENV=()

# assert_head_is_pin, extracted verbatim from the script under test
AH_FILE="$WORK/ah.sh"
awk '/^assert_head_is_pin\(\) \{/ { on = 1 } on { print } on && /^\}/ { exit }' "$SUT" >"$AH_FILE"
G init -q "$WORK/headrepo"
echo x >"$WORK/headrepo/f"
G -C "$WORK/headrepo" add f
G -C "$WORK/headrepo" commit -qm c
HEAD_SHA="$(G -C "$WORK/headrepo" rev-parse HEAD)"
# shellcheck disable=SC1090 # sources a generated file
ah() { (. "$AH_FILE" && assert_head_is_pin "$@") >/dev/null 2>&1; }
if ah "$WORK/headrepo" "$HEAD_SHA" && ah "$WORK/headrepo" "$(tr a-f A-F <<<"$HEAD_SHA")"; then pass "assert_head_is_pin: HEAD == pin passes"; else fail "assert_head_is_pin positive"; fi
if ! ah "$WORK/headrepo" "$SHA" && ! ah "$WORK/not-a-repo" "$SHA"; then pass "assert_head_is_pin: a different HEAD (or no repo) fails"; else fail "assert_head_is_pin negative"; fi

# --- F7: --strict fails on a dirty worktree, and uppercase pins compare equal ---
G init -q "$WORK/siblings/mod-core"
echo x >"$WORK/siblings/mod-core/f"
G -C "$WORK/siblings/mod-core" add f
G -C "$WORK/siblings/mod-core" commit -qm c
SIB_SHA="$(G -C "$WORK/siblings/mod-core" rev-parse HEAD)"
run_sut notoken "$(rawlock "  mod-core: $SIB_SHA\n")" --verify --strict
if [ "$RC" -eq 0 ]; then pass "--strict: a clean sibling at its pin is ok"; else fail "--strict clean: rc=$RC out='$OUT'"; fi
run_sut notoken "$(rawlock "  mod-core: $(tr a-f A-F <<<"$SIB_SHA")\n")" --verify --strict
if [ "$RC" -eq 0 ]; then pass "an upper-case pin SHA compares equal to HEAD (normalized to lower case)"; else fail "uppercase pin: rc=$RC out='$OUT'"; fi
echo y >"$WORK/siblings/mod-core/untracked"
run_sut notoken "$(rawlock "  mod-core: $SIB_SHA\n")" --verify --strict
if [ "$RC" -eq 1 ] && grep -q 'worktree is dirty' <<<"$OUT"; then pass "--strict: a dirty worktree exits 1"; else fail "--strict dirty: rc=$RC out='$OUT'"; fi
run_sut notoken "$(rawlock "  mod-core: $SIB_SHA\n")" --verify
if [ "$RC" -eq 0 ]; then pass "--verify without --strict still exits 0 on a dirty worktree"; else fail "non-strict dirty: rc=$RC"; fi
rm -rf "${WORK:?}/siblings" && mkdir -p "$WORK/siblings"

# --- F6: update-pins refuses a sibling whose origin is not the lockfile's host/owner ---
rm -rf "${WORK:?}/usib" && mkdir -p "$WORK/usib"
mk_usib() { # mk_usib NAME ORIGIN_URL -> a sibling repo with a commit and refs/remotes/origin/main at HEAD
	G init -q "$WORK/usib/$1"
	echo "$1" >"$WORK/usib/$1/f"
	G -C "$WORK/usib/$1" add f
	G -C "$WORK/usib/$1" commit -qm "c1 $1"
	G -C "$WORK/usib/$1" remote add origin "$2"
	G -C "$WORK/usib/$1" update-ref refs/remotes/origin/main HEAD
}
lock="$(rawlock "  mod-core: $SHA\n")"
for url in 'https://github.com/evil/mod-core.git' 'git@github.com:evil/mod-core.git' 'https://evil.example/moduleforge/mod-core.git' \
	'https://user:SECRETTOKEN@evil.example/moduleforge/mod-core.git' '/some/local/path/mod-core' 'https://github.com.evil.example/moduleforge/mod-core.git'; do
	rm -rf "${WORK:?}/usib/mod-core"
	mk_usib mod-core "$url"
	run_update_pins "$lock" --dry-run mod-core
	if [ "$RC" -eq 3 ] && no_fetch && ! grep -q SECRETTOKEN <<<"$OUT" && grep -Eq "lockfile says|cannot parse" <<<"$OUT"; then
		pass "update-pins refuses origin '${url%%SECRETTOKEN*}...': exit 3, no fetch, URL not echoed"
	else
		fail "update-pins origin '$url': rc=$RC out='$OUT' log='$LOG'"
	fi
done
for url in 'git@github.com:moduleforge/mod-core.git' 'https://github.com/moduleforge/mod-core.git' 'ssh://git@github.com/moduleforge/mod-core' \
	'https://tok@GitHub.com/ModuleForge/mod-core.git/'; do
	rm -rf "${WORK:?}/usib/mod-core"
	mk_usib mod-core "$url"
	run_update_pins "$lock" --dry-run mod-core
	if [ "$RC" -eq 0 ] && grep -q 'dry-run' <<<"$OUT"; then pass "update-pins accepts origin '$url'"; else fail "update-pins origin '$url': rc=$RC out='$OUT'"; fi
done
# an overrides owner changes what origin must be
OVL="$(rawlock "  mod-core: $SHA\n\noverrides:\n  mod-core:\n    owner: forkowner\n")"
rm -rf "${WORK:?}/usib/mod-core"
mk_usib mod-core 'git@github.com:moduleforge/mod-core.git'
run_update_pins "$OVL" --dry-run mod-core
if [ "$RC" -eq 3 ]; then pass "update-pins: with an owner override, the top-level owner's origin is refused"; else fail "override owner mismatch: rc=$RC out='$OUT'"; fi
rm -rf "${WORK:?}/usib/mod-core"
mk_usib mod-core 'git@github.com:forkowner/mod-core.git'
run_update_pins "$OVL" --dry-run mod-core
if [ "$RC" -eq 0 ]; then pass "update-pins: the override owner's origin is accepted"; else fail "override owner match: rc=$RC out='$OUT'"; fi

# --- F4: update-pins rewrites the key literally, exactly one line -----------
rm -rf "${WORK:?}/usib" && mkdir -p "$WORK/usib"
mk_usib mod.core 'git@github.com:moduleforge/mod.core.git'
lock="$(rawlock "  mod.core: $SHA  # old\n  modXcore: $SHA  # old\n")"
cp "$lock" "$WORK/f4.yaml"
run_update_pins "$WORK/f4.yaml" mod.core
NEW_CORE="$(G -C "$WORK/usib/mod.core" rev-parse HEAD)"
if [ "$RC" -eq 0 ] && grep -q "^  mod.core: $NEW_CORE  # " "$WORK/f4.yaml" && grep -q "^  modXcore: $SHA  # old$" "$WORK/f4.yaml"; then
	pass "update-pins: key 'mod.core' is literal; the look-alike 'modXcore' line is untouched"
else
	fail "update-pins literal key: rc=$RC out='$OUT' file='$(cat "$WORK/f4.yaml")'"
fi

UPL_FILE="$WORK/upl.sh"
for fn in err valid_repo update_pin_line; do
	awk -v fn="$fn" '!on && $0 ~ "^" fn "\\(\\) \\{" { on = 1 } on { print } on && /^\}/ { on = 0 }' "$UPDATE_PINS" "$LIB"
done >"$UPL_FILE"
# (update_pin_line itself, driven directly with a file the parser would have refused)
printf '  abc: %s\n  a.c: %s  # x\n  a.c: %s\n' "$SHA" "$SHA" "$SHA" >"$WORK/upl.yaml"
RC=0
# shellcheck disable=SC1090 # sources a generated file
(. "$UPL_FILE" && update_pin_line "$WORK/upl.yaml" a.c "$SHA2" "new") >/dev/null 2>&1 || RC=$?
if [ "$RC" -eq 0 ] && [ "$(sed -n 1p "$WORK/upl.yaml")" = "  abc: $SHA" ] && [ "$(sed -n 2p "$WORK/upl.yaml")" = "  a.c: $SHA2  # new" ] &&
	[ "$(sed -n 3p "$WORK/upl.yaml")" = "  a.c: $SHA" ]; then
	pass "update_pin_line: exact key only (no regex wildcard) and only the first matching line"
else
	fail "update_pin_line literal: rc=$RC file='$(cat "$WORK/upl.yaml")'"
fi
for badkey in '..' 'a b' '-x' 'a.*'; do
	printf '  ok: %s\n' "$SHA" >"$WORK/upl.yaml"
	RC=0
	# shellcheck disable=SC1090 # sources a generated file
	(. "$UPL_FILE" && update_pin_line "$WORK/upl.yaml" "$badkey" "$SHA2" "new") >/dev/null 2>&1 || RC=$?
	if [ "$RC" -eq 3 ] && [ "$(cat "$WORK/upl.yaml")" = "  ok: $SHA" ]; then pass "update_pin_line: invalid key '$badkey' is refused, file untouched"; else fail "update_pin_line key '$badkey': rc=$RC"; fi
done
rm -rf "${WORK:?}/usib"

# --- the repository's own lockfile still passes -----------------------------
ROOT_LOCK="$SCRIPT_DIR/../versions.lock.yaml"
run_sut token "$ROOT_LOCK" --verify --remote
if [ "$RC" -eq 0 ] && no_leak; then pass "versions.lock.yaml passes the guard"; else fail "versions.lock.yaml: rc=$RC"; fi

if [ "$FAILS" -ne 0 ]; then
	printf 'test-checkout-deps: %d case(s) failed\n' "$FAILS" >&2
	exit 1
fi
echo "test-checkout-deps: all cases passed"
