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
		elif [ "$RC" -eq 0 ] && grep -q 'hdr_ok=1' <<<"$LOG" && no_leak; then
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
	' "$SUT"
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

# --- the repository's own lockfile still passes -----------------------------
ROOT_LOCK="$SCRIPT_DIR/../versions.lock.yaml"
run_sut token "$ROOT_LOCK" --verify --remote
if [ "$RC" -eq 0 ] && no_leak; then pass "versions.lock.yaml passes the guard"; else fail "versions.lock.yaml: rc=$RC"; fi

if [ "$FAILS" -ne 0 ]; then
	printf 'test-checkout-deps: %d case(s) failed\n' "$FAILS" >&2
	exit 1
fi
echo "test-checkout-deps: all cases passed"
