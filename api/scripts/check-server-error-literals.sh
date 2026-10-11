#!/usr/bin/env bash
# check-server-error-literals.sh — guard against server.Error/
# server.ErrorWithDetails literal-call drift in api/internal/handlers/.
#
# Background: the centralize-server-error plan collapsed 92 of 96 literal
# server.Error(...)/server.ErrorWithDetails(...) call sites in the top-level
# api/internal/handlers/*.go package onto apiresp.WriteError(w, r, err), which
# classifies errors via sentinel matching instead of hand-picked HTTP
# status/code/message literals; the provider-exists 409 has since moved onto
# apiresp.Conflict(...) too. Exactly 3 documented carve-outs remain (marked
# by a comment), each justified by a comment referencing followup ZVum:
# apiresp currently exposes no public detail-carrying constructor for the
# specific error shapes those 3 sites need (a 404 with details and two
# authentication-detail-carrying 401s, all in identities.go), so they cannot
# be routed through apiresp.WriteError without losing information.
#
# This script re-greps every build and fails the build if a new literal call
# to server.Error(/server.ErrorWithDetails( shows up anywhere else in that
# top-level package, so future handlers don't quietly reintroduce the pattern
# apiresp.WriteError was meant to replace.
#
# The grep is anchored on the actual call site (`server\.Error(w,` /
# `server\.ErrorWithDetails(w,`), not a raw substring match: a carve-out's own
# justification comment can legitimately contain the substring
# "server.ErrorWithDetails" in prose, and a raw substring grep would count
# that comment as a hit and desync from the real call-site count (see
# followup 6vir).
#
# Usage: check-server-error-literals.sh
# Run from the api/ directory (or invoked via `make lint` in api/Makefile).

set -u
set -o pipefail

# Resolve the handlers directory relative to this script's location so the
# check works regardless of the caller's cwd.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ ! -d "$SCRIPT_DIR/../internal/handlers" ]]; then
  echo "check-server-error-literals: handlers directory not found: $SCRIPT_DIR/../internal/handlers" >&2
  exit 2
fi

HANDLERS_DIR="$(cd "$SCRIPT_DIR/../internal/handlers" && pwd)"

# Carve-outs are keyed on a stable in-source marker, not line numbers: a
# literal call is allowed only when a "followup ZVum" justification comment
# appears within MARKER_WINDOW lines above it (each marker covers one call).
# Edits elsewhere in a file therefore never desync the check.
MARKER='followup ZVum'
MARKER_WINDOW=8
EXPECTED_CARVEOUTS=3

# Collect every top-level *.go file directly in handlers/ (glob does not
# descend into subdirectories, so handlers/auth/ is excluded automatically),
# excluding _test.go files.
shopt -s nullglob
files=()
for f in "$HANDLERS_DIR"/*.go; do
  [[ "$f" == *_test.go ]] && continue
  files+=("$f")
done
shopt -u nullglob

if [[ ${#files[@]} -eq 0 ]]; then
  echo "check-server-error-literals: no source files found in $HANDLERS_DIR" >&2
  exit 2
fi

# Call-site-anchored match: require the literal token immediately followed by
# "(w," (the http.ResponseWriter argument), not just the bare package.symbol
# substring. This is what keeps a justification comment mentioning
# "server.ErrorWithDetails" in prose from being counted as a hit.
# Emit "<status> <path>:<line>" per call site (status ok|bad); a call is ok
# when an unconsumed marker comment precedes it within MARKER_WINDOW lines.
results="$(awk -v marker="$MARKER" -v win="$MARKER_WINDOW" '
  FNR == 1 { m = 0 }
  index($0, marker) { m = FNR }
  /server\.(Error|ErrorWithDetails)\(w,/ {
    if (m > 0 && FNR - m <= win) { print "ok " FILENAME ":" FNR; m = 0 }
    else print "bad " FILENAME ":" FNR
  }
' "${files[@]}")"

violations=()
ok_count=0
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  if [[ "${line%% *}" == "ok" ]]; then
    ok_count=$((ok_count + 1))
  else
    violations+=("${line#* }")
  fi
done <<< "$results"

if [[ ${#violations[@]} -gt 0 ]]; then
  echo "check-server-error-literals: found literal server.Error/server.ErrorWithDetails call(s) outside the documented allowlist:" >&2
  for v in "${violations[@]}"; do
    echo "  $v" >&2
  done
  echo "" >&2
  echo "Handlers in api/internal/handlers/ must route errors through apiresp.WriteError(w, r, err)" >&2
  echo "instead of calling server.Error(...)/server.ErrorWithDetails(...) directly, so error" >&2
  echo "classification stays centralized on the sentinel set apiresp.WriteError understands." >&2
  echo "Exactly 3 documented carve-outs are allowed (each marked by a 'followup ZVum'" >&2
  echo "comment within $MARKER_WINDOW lines above the call), because apiresp currently has no public" >&2
  echo "detail-carrying constructor for those specific error shapes." >&2
  echo "If this is a new, similarly-justified carve-out, add a 'followup ZVum'-style comment" >&2
  echo "above the call site and bump EXPECTED_CARVEOUTS in" >&2
  echo "api/scripts/check-server-error-literals.sh. Otherwise, replace the literal call with" >&2
  echo "apiresp.WriteError(w, r, err)." >&2
  exit 1
fi

# Detect drift: the number of marked carve-outs must match the documented count.
if [[ $ok_count -ne $EXPECTED_CARVEOUTS ]]; then
  echo "check-server-error-literals: expected $EXPECTED_CARVEOUTS marked carve-outs, found $ok_count." >&2
  echo "Update EXPECTED_CARVEOUTS in api/scripts/check-server-error-literals.sh if this is intentional." >&2
  exit 1
fi

echo "check-server-error-literals: ok ($ok_count/$EXPECTED_CARVEOUTS documented carve-outs accounted for, no undocumented literal calls)"
exit 0
