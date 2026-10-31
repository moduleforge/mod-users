import { afterEach, describe, expect, test } from 'bun:test';
import {
  beginHandoff,
  clearHandoffVerifier,
  computeHandoffChallenge,
  generateHandoffVerifier,
  readHandoffVerifier,
} from './handoff';
import { getStoredHandoffVerifier, resetUsersApiConfig } from './config';

afterEach(() => {
  resetUsersApiConfig();
  sessionStorage.clear();
});

describe('verifier generation', () => {
  test('is 43 base64url characters, inside the API accepted range', () => {
    const verifier = generateHandoffVerifier();
    expect(verifier).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(verifier).toMatch(/^[A-Za-z0-9._~-]{43,128}$/);
  });

  test('is random per call', () => {
    const seen = new Set(Array.from({ length: 50 }, () => generateHandoffVerifier()));
    expect(seen.size).toBe(50);
  });
});

describe('challenge', () => {
  test('is the unpadded base64url SHA-256 of the verifier (RFC 7636 appendix B vector)', async () => {
    expect(await computeHandoffChallenge('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk')).toBe(
      'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM',
    );
  });

  test('is 43 characters and differs per verifier', async () => {
    const a = await computeHandoffChallenge(generateHandoffVerifier());
    const b = await computeHandoffChallenge(generateHandoffVerifier());
    expect(a).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(a).not.toBe(b);
  });
});

describe('beginHandoff', () => {
  test('stores the verifier for this tab and returns its challenge', async () => {
    const challenge = await beginHandoff();
    const verifier = readHandoffVerifier();
    expect(verifier).not.toBeNull();
    expect(challenge).toBe(await computeHandoffChallenge(verifier!));
    expect(sessionStorage.getItem('auth_token.handoff_verifier')).toBe(verifier!);
    expect(localStorage.length).toBe(0);
  });

  test('a second attempt replaces the first verifier (latest click wins)', async () => {
    await beginHandoff();
    const first = readHandoffVerifier();
    await beginHandoff();
    expect(readHandoffVerifier()).not.toBe(first);
  });

  test('clearHandoffVerifier forgets the attempt', async () => {
    await beginHandoff();
    clearHandoffVerifier();
    expect(readHandoffVerifier()).toBeNull();
    expect(getStoredHandoffVerifier()).toBeNull();
  });
});
