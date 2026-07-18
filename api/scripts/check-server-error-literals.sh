#!/usr/bin/env bash
# check-server-error-literals.sh — guard against server.Error/
# server.ErrorWithDetails literal-call drift in api/internal/handlers/.
#
# Background: the centralize-server-error plan collapsed 92 of 96 literal
# server.Error(...)/server.ErrorWithDetails(...) call sites in the top-level
# api/internal/handlers/*.go package onto apiresp.WriteError(w, r, err), which
# classifies errors via sentinel matching instead of hand-picked HTTP
# status/code/message literals. Exactly 4 documented carve-outs remain (see
# ALLOWLIST below), each justified by a comment referencing followup ZVum:
# apiresp currently exposes no public detail/message-carrying constructor for
# the specific error shapes those 4 sites need (409 conflict with a custom
# message, and two authentication-detail-carrying 401s), so they cannot be
# routed through apiresp.WriteError without losing information.
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

# Documented carve-out call sites (file:line), each with an in-source
# justification comment referencing followup ZVum. Update this list only in
# lockstep with adding/removing/moving a justified literal call.
ALLOWLIST=(
  "oidc_providers.go:222"
  "identities.go:310"
  "identities.go:389"
  "identities.go:407"
)

is_allowlisted() {
  local candidate="$1"
  local entry
  for entry in "${ALLOWLIST[@]}"; do
    if [[ "$candidate" == "$entry" ]]; then
      return 0
    fi
  done
  return 1
}

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
matches="$(grep -n -E 'server\.(Error|ErrorWithDetails)\(w,' "${files[@]}" 2>/dev/null || true)"

violations=()
seen_allowlisted=()

if [[ -n "$matches" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    # grep -n with multiple files prefixes: <path>:<lineno>:<content>
    fullpath="${line%%:*}"
    rest="${line#*:}"
    lineno="${rest%%:*}"
    base="$(basename "$fullpath")"
    key="${base}:${lineno}"
    if is_allowlisted "$key"; then
      seen_allowlisted+=("$key")
    else
      violations+=("$fullpath:$lineno")
    fi
  done <<< "$matches"
fi

if [[ ${#violations[@]} -gt 0 ]]; then
  echo "check-server-error-literals: found literal server.Error/server.ErrorWithDetails call(s) outside the documented allowlist:" >&2
  for v in "${violations[@]}"; do
    echo "  $v" >&2
  done
  echo "" >&2
  echo "Handlers in api/internal/handlers/ must route errors through apiresp.WriteError(w, r, err)" >&2
  echo "instead of calling server.Error(...)/server.ErrorWithDetails(...) directly, so error" >&2
  echo "classification stays centralized on the sentinel set apiresp.WriteError understands." >&2
  echo "Exactly 4 documented carve-outs are allowed (see ALLOWLIST in this script and the" >&2
  echo "'followup ZVum' comment at each site), because apiresp currently has no public" >&2
  echo "detail/message-carrying constructor for those specific error shapes." >&2
  echo "If this is a new, similarly-justified carve-out, add a 'followup ZVum'-style comment" >&2
  echo "at the call site and add its file:line to ALLOWLIST in" >&2
  echo "api/scripts/check-server-error-literals.sh. Otherwise, replace the literal call with" >&2
  echo "apiresp.WriteError(w, r, err)." >&2
  exit 1
fi

# Detect allowlist drift: if a hardcoded entry no longer corresponds to an
# actual call site (e.g. the line moved), the allowlist is stale and the
# check may no longer be doing what it claims.
missing=()
for entry in "${ALLOWLIST[@]}"; do
  found=0
  for s in "${seen_allowlisted[@]}"; do
    if [[ "$s" == "$entry" ]]; then
      found=1
      break
    fi
  done
  if [[ $found -eq 0 ]]; then
    missing+=("$entry")
  fi
done

if [[ ${#missing[@]} -gt 0 ]]; then
  echo "check-server-error-literals: allowlist entries do not match any current call site (line drift?):" >&2
  for m in "${missing[@]}"; do
    echo "  $m" >&2
  done
  echo "Re-verify the carve-out line numbers and update ALLOWLIST in api/scripts/check-server-error-literals.sh." >&2
  exit 1
fi

echo "check-server-error-literals: ok (4/4 documented carve-outs accounted for, no undocumented literal calls)"
exit 0
