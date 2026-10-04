import { getUnauthenticatedReturnParam, isSafeReturnPath } from './config';

// The single return-path predicate lives in config.ts (which imports no other
// lib module) so the 401 writer and this reader can never diverge.
export { isSafeReturnPath };

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
