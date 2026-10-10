#!/usr/bin/env bash
# scripts/test-checkout-deps.sh — tests for the lockfile and token guards in
# scripts/checkout-deps.sh: a hostile `host:` / `owner:` (top-level or in an
# `overrides` entry) in the git-tracked versions.lock.yaml must never receive
# the read token, the auth header must be scoped to the fetch target's host,
# redirects must be off, and the token must never appear on a command line
# (checked against the live process table at fetch time, not via `bash -x`).
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
		bash "$SUT" --lockfile "$lock" --into "$WORK/siblings" "$@" 2>&1)" || RC=$?
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

# --- the repository's own lockfile still passes -----------------------------
ROOT_LOCK="$SCRIPT_DIR/../versions.lock.yaml"
run_sut token "$ROOT_LOCK" --verify --remote
if [ "$RC" -eq 0 ] && no_leak; then pass "versions.lock.yaml passes the guard"; else fail "versions.lock.yaml: rc=$RC"; fi

if [ "$FAILS" -ne 0 ]; then
	printf 'test-checkout-deps: %d case(s) failed\n' "$FAILS" >&2
	exit 1
fi
echo "test-checkout-deps: all cases passed"
