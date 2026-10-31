// PKCE-style binding of the OIDC handoff code to the browser tab that started
// the sign-in. The tab keeps a random verifier and sends only its SHA-256
// challenge to the OIDC start route; the one-time `#handoff=` code the callback
// later issues is redeemable only together with that verifier, so a code
// intercepted from the redirect URL is useless to anyone else.

import {
  clearStoredHandoffVerifier,
  getStoredHandoffVerifier,
  setStoredHandoffVerifier,
} from './config';

/** Random bytes behind a verifier: 32 bytes is 43 base64url characters. */
const VERIFIER_BYTES = 32;

function webCrypto(): Crypto {
  const c = globalThis.crypto;
  // Without Web Crypto there is no safe fallback: a weak verifier would
  // silently defeat the binding, so refuse instead.
  if (!c || typeof c.getRandomValues !== 'function' || !c.subtle) {
    throw new Error('Secure sign-in is unavailable: this browser has no Web Crypto support.');
  }
  return c;
}

/** Unpadded base64url of `bytes` (the encoding the API expects for both values). */
function base64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/**
 * A cryptographically random verifier: 43 characters of `[A-Za-z0-9_-]`, inside
 * the API's accepted range of 43 to 128 `[A-Za-z0-9-._~]` characters.
 */
export function generateHandoffVerifier(): string {
  const bytes = new Uint8Array(VERIFIER_BYTES);
  webCrypto().getRandomValues(bytes);
  return base64Url(bytes);
}

/** The challenge for `verifier`: unpadded base64url SHA-256 (43 characters). */
export async function computeHandoffChallenge(verifier: string): Promise<string> {
  const digest = await webCrypto().subtle.digest(
    'SHA-256',
    new TextEncoder().encode(verifier),
  );
  return base64Url(new Uint8Array(digest));
}

/**
 * Starts a sign-in attempt: generates a fresh verifier, stores it for this tab
 * only (sessionStorage, or memory), and returns the challenge to send on the
 * OIDC start request. A tab has one pending attempt; starting another
 * replaces the earlier verifier, which makes the earlier attempt's code
 * unredeemable (the intended behavior: the latest click wins).
 */
export async function beginHandoff(): Promise<string> {
  const verifier = generateHandoffVerifier();
  const challenge = await computeHandoffChallenge(verifier);
  // Store only after the challenge computed, so a failure leaves any earlier
  // pending attempt intact rather than half-replaced.
  setStoredHandoffVerifier(verifier);
  return challenge;
}

/** The pending attempt's verifier, or null when this tab started none. */
export function readHandoffVerifier(): string | null {
  return getStoredHandoffVerifier();
}

/** Forgets the pending attempt's verifier (after the exchange, success or not). */
export function clearHandoffVerifier(): void {
  clearStoredHandoffVerifier();
}
