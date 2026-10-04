import { getUnauthenticatedReturnParam } from './config';

/**
 * Validates that a candidate return path is a safe, same-origin relative
 * path. Rejects absolute URLs, protocol-relative URLs (`//evil.com`), and
 * any string attempting to embed a scheme before the first slash.
 *
 * Exported so consuming apps can validate their own `?return=` values with
 * the same rules `OidcCallbackPage` applies to the OIDC `return` fragment
 * value, before navigating to them.
 */
export function isSafeReturnPath(candidate: string | null): candidate is string {
  if (!candidate) return false;
  // Reject embedded C0 control characters (tab, LF, CR, etc.). The WHATWG
  // URL parser strips these from raw URL text at parse time, so a candidate
  // like `/\t/evil.com` collapses into a protocol-relative `//evil.com` when
  // later passed to `new URL(...)` or similar, bypassing the `//`-prefix and
  // `\`-character checks below.
  if (/[\x00-\x1F\x7F]/.test(candidate)) return false;
  // Must start with exactly one '/'.
  if (!candidate.startsWith('/')) return false;
  // Reject protocol-relative paths like `//evil.com/foo`.
  if (candidate.startsWith('//')) return false;
  // Reject backslashes anywhere in the path. Some legacy browser URL parsers
  // normalize `\` to `/`, so `/\evil.com` can be interpreted as `//evil.com`
  // and trigger an open redirect. Belt-and-suspenders: disallow `\` outright.
  if (candidate.includes('\\')) return false;
  // Reject anything trying to sneak a scheme in before the path separator,
  // e.g. `/\x0Ajavascript:...` variants or `javascript:...`. Since we already
  // require a leading `/`, a `:` anywhere in the first segment is suspicious.
  const firstSlashAfterStart = candidate.indexOf('/', 1);
  const firstSegment =
    firstSlashAfterStart === -1
      ? candidate.slice(1)
      : candidate.slice(1, firstSlashAfterStart);
  if (firstSegment.includes(':')) return false;
  return true;
}

/**
 * Reads the post-login return path the library wrote on a 401 redirect.
 * Returns `null` unless `unauthenticatedReturnParam` is configured and a
 * window exists; otherwise returns the configured query param's value from
 * `window.location.search` only when `isSafeReturnPath` accepts it. Reads at
 * call time; holds no module state.
 */
export function readReturnPath(): string | null {
  const param = getUnauthenticatedReturnParam();
  if (param === null || typeof window === 'undefined') return null;
  const value = new URLSearchParams(window.location.search).get(param);
  return isSafeReturnPath(value) ? value : null;
}
