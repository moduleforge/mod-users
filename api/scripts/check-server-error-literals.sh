#!/usr/bin/env bash
# check-server-error-literals.sh — guard against server.Error/
# server.ErrorWithDetails literal-call drift in api/internal/handlers/ and
# api/internal/handlers/auth/.
#
# Background: the centralize-server-error plan collapsed the literal
# server.Error(...)/server.ErrorWithDetails(...) call sites in the handlers
# onto apiresp.WriteError(w, r, err), which classifies errors via sentinel
# matching instead of hand-picked HTTP status/code/message literals. The
# identity-model rewrite then removed the last documented carve-outs (the
# identities.go detail-carrying shapes and the public auth credential-failure
# and register literals), so no marked carve-out remains in either package:
# the expected count is 0 and every literal call is a violation.
#
# The public auth package (api/internal/handlers/auth/) is scanned separately
# with its own marker ("literal-keep:") and count.
#
# This script re-greps every build and fails the build if a new literal call
# to server.Error(/server.ErrorWithDetails( shows up anywhere else in those
# packages, so future handlers don't quietly reintroduce the pattern
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

# Resolve the handlers directories relative to this script's location so the
# check works regardless of the caller's cwd.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for d in "$SCRIPT_DIR/../internal/handlers" "$SCRIPT_DIR/../internal/handlers/auth"; do
  if [[ ! -d "$d" ]]; then
    echo "check-server-error-literals: handlers directory not found: $d" >&2
    exit 2
  fi
done

HANDLERS_DIR="$(cd "$SCRIPT_DIR/../internal/handlers" && pwd)"
AUTH_HANDLERS_DIR="$(cd "$SCRIPT_DIR/../internal/handlers/auth" && pwd)"

# Carve-outs are keyed on a stable in-source marker, not line numbers: a
# literal call is allowed only when a marker justification comment appears
# within MARKER_WINDOW lines above it (each marker covers one call). Edits
# elsewhere in a file therefore never desync the check.
MARKER_WINDOW=8

# check_dir <label> <dir> <marker> <expected>
# Collects every *.go file directly in <dir> (the glob does not descend into
# subdirectories), excluding _test.go files, and verifies that every literal
# call is marked and that the marked count equals <expected>.
check_dir() {
  local label="$1" dir="$2" marker="$3" expected="$4"

  shopt -s nullglob
  local files=() f
  for f in "$dir"/*.go; do
    [[ "$f" == *_test.go ]] && continue
    files+=("$f")
  done
  shopt -u nullglob

  if [[ ${#files[@]} -eq 0 ]]; then
    echo "check-server-error-literals: no source files found in $dir" >&2
    return 2
  fi

  # Call-site-anchored match: require the literal token immediately followed
  # by "(w," (the http.ResponseWriter argument), not just the bare
  # package.symbol substring. This is what keeps a justification comment
  # mentioning "server.ErrorWithDetails" in prose from being counted as a
  # hit. Emit "<status> <path>:<line>" per call site (status ok|bad); a call
  # is ok when an unconsumed marker comment precedes it within MARKER_WINDOW
  # lines.
  local results
  results="$(awk -v marker="$marker" -v win="$MARKER_WINDOW" '
    FNR == 1 { m = 0 }
    index($0, marker) { m = FNR }
    /server\.(Error|ErrorWithDetails)\(w,/ {
      if (m > 0 && FNR - m <= win) { print "ok " FILENAME ":" FNR; m = 0 }
      else print "bad " FILENAME ":" FNR
    }
  ' "${files[@]}")"

  local violations=() ok_count=0 line
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    if [[ "${line%% *}" == "ok" ]]; then
      ok_count=$((ok_count + 1))
    else
      violations+=("${line#* }")
    fi
  done <<< "$results"

  if [[ ${#violations[@]} -gt 0 ]]; then
    echo "check-server-error-literals: found literal server.Error/server.ErrorWithDetails call(s) outside the documented allowlist in $label:" >&2
    for v in "${violations[@]}"; do
      echo "  $v" >&2
    done
    echo "" >&2
    echo "Handlers must route errors through apiresp.WriteError(w, r, err) instead of calling" >&2
    echo "server.Error(...)/server.ErrorWithDetails(...) directly, so error classification stays" >&2
    echo "centralized on the sentinel set apiresp.WriteError understands." >&2
    echo "Exactly $expected documented carve-outs are allowed in $label (each marked by a '$marker'" >&2
    echo "comment within $MARKER_WINDOW lines above the call)." >&2
    echo "If this is a new, similarly-justified carve-out, add a '$marker' comment" >&2
    echo "above the call site and bump the expected count in" >&2
    echo "api/scripts/check-server-error-literals.sh. Otherwise, replace the literal call with" >&2
    echo "apiresp.WriteError(w, r, err)." >&2
    return 1
  fi

  # Detect drift: the number of marked carve-outs must match the documented count.
  if [[ $ok_count -ne $expected ]]; then
    echo "check-server-error-literals: expected $expected marked carve-outs in $label, found $ok_count." >&2
    echo "Update the expected count in api/scripts/check-server-error-literals.sh if this is intentional." >&2
    return 1
  fi

  echo "check-server-error-literals: ok ($label: $ok_count/$expected documented carve-outs accounted for, no undocumented literal calls)"
  return 0
}

status=0
# Top-level handlers: no documented carve-outs remain.
check_dir "handlers/" "$HANDLERS_DIR" 'followup ZVum' 0 || status=$?
# Public auth handlers: no documented carve-outs remain.
check_dir "handlers/auth/" "$AUTH_HANDLERS_DIR" 'literal-keep:' 0 || { s=$?; [[ $status -eq 0 || $s -gt $status ]] && status=$s; }
exit "$status"
