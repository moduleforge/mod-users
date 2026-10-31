import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { forkFromSibling, startForkResponder, FORK_CHANNEL_PREFIX } from './fork';
import type { TokenResponse } from './api';
import {
  configureUsersApi,
  getStoredRefreshToken,
  getStoredToken,
  resetUsersApiConfig,
} from './config';

// A same-name in-process bus standing in for BroadcastChannel: a message
// posted on one instance reaches every OTHER open instance of that name,
// asynchronously, like the real thing.
class FakeBroadcastChannel {
  static open = new Map<string, Set<FakeBroadcastChannel>>();
  static posted: unknown[] = [];
  onmessage: ((event: MessageEvent) => void) | null = null;
  closed = false;

  constructor(public name: string) {
    const peers = FakeBroadcastChannel.open.get(name) ?? new Set();
    peers.add(this);
    FakeBroadcastChannel.open.set(name, peers);
  }

  postMessage(data: unknown): void {
    FakeBroadcastChannel.posted.push(data);
    for (const peer of FakeBroadcastChannel.open.get(this.name) ?? []) {
      if (peer === this || peer.closed) continue;
      queueMicrotask(() => {
        if (!peer.closed) peer.onmessage?.({ data } as MessageEvent);
      });
    }
  }

  close(): void {
    this.closed = true;
    FakeBroadcastChannel.open.get(this.name)?.delete(this);
  }
}

const realBroadcastChannel = (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel;
const originalFetch = globalThis.fetch;

beforeEach(() => {
  FakeBroadcastChannel.open.clear();
  FakeBroadcastChannel.posted = [];
  (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = FakeBroadcastChannel;
});

afterEach(() => {
  (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = realBroadcastChannel;
  globalThis.fetch = originalFetch;
  resetUsersApiConfig();
  sessionStorage.clear();
});

const FORKED: TokenResponse = {
  access_token: 'forked-access',
  token_type: 'Bearer',
  expires_in: 3600,
  refresh_token: 'forked-refresh',
  session: { uuid: 's1', acr: 1, amr: ['pwd'], auth_time: 'x', impersonating: false },
  profile: {
    uuid: 'prof-a',
    kind: 'personal',
    party: null,
    status: 'active',
    display_name: 'Ada',
    available: true,
    lock: null,
  },
  selection_required: false,
  profiles: [],
};

interface ForkCall {
  url: string;
  authorization: string | undefined;
}

function stubFork(
  respond: (call: ForkCall, index: number) => Response,
): ForkCall[] {
  const calls: ForkCall[] = [];
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    const call = {
      url: String(url),
      authorization: (init?.headers as Record<string, string>)['Authorization'],
    };
    calls.push(call);
    return respond(call, calls.length - 1);
  }) as unknown as typeof fetch;
  return calls;
}

const ok = (): Response =>
  new Response(JSON.stringify(FORKED), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
const rejected = (): Response =>
  new Response(JSON.stringify({ error: { code: 'unauthorized', message: 'no' } }), {
    status: 401,
    headers: { 'Content-Type': 'application/json' },
  });

describe('forkFromSibling', () => {
  test('asks siblings for an access token and forks its own lineage with it', async () => {
    const calls = stubFork(ok);
    const stop = startForkResponder(() => 'sibling-access');
    const forked = await forkFromSibling({ timeoutMs: 500 });
    stop();
    expect(forked).toBe(true);
    expect(calls.length).toBe(1);
    expect(calls[0]!.url).toContain('/v1/session/fork');
    expect(calls[0]!.authorization).toBe('Bearer sibling-access');
    expect(getStoredToken()).toBe('forked-access');
    expect(getStoredRefreshToken()).toBe('forked-refresh');
  });

  test('only access tokens cross the channel, never refresh tokens', async () => {
    sessionStorage.setItem('auth_token.refresh', 'sibling-refresh-secret');
    stubFork(ok);
    const stop = startForkResponder(() => 'sibling-access');
    await forkFromSibling({ timeoutMs: 500 });
    stop();
    const wire = JSON.stringify(FakeBroadcastChannel.posted);
    expect(wire).toContain('sibling-access');
    expect(wire).not.toContain('sibling-refresh-secret');
    expect(wire).not.toContain('forked-refresh');
  });

  test('no answer within the timeout leaves the tab signed out', async () => {
    const calls = stubFork(ok);
    const started = Date.now();
    const forked = await forkFromSibling({ timeoutMs: 40 });
    expect(forked).toBe(false);
    expect(Date.now() - started).toBeGreaterThanOrEqual(30);
    expect(calls.length).toBe(0);
    expect(getStoredToken()).toBeNull();
  });

  test('uses the configured fork timeout', async () => {
    configureUsersApi({ forkTimeoutMs: 30 });
    const started = Date.now();
    expect(await forkFromSibling()).toBe(false);
    expect(Date.now() - started).toBeLessThan(500);
  });

  test('a sibling without a session stays silent, so it is never forked from', async () => {
    const calls = stubFork(ok);
    const stop = startForkResponder(() => null);
    expect(await forkFromSibling({ timeoutMs: 40 })).toBe(false);
    stop();
    expect(calls.length).toBe(0);
  });

  test('falls through to the next sibling when the first token is refused', async () => {
    const calls = stubFork((call) => (call.authorization === 'Bearer good' ? ok() : rejected()));
    const stopBad = startForkResponder(() => 'stale');
    const stopGood = startForkResponder(() => 'good');
    const forked = await forkFromSibling({ timeoutMs: 500 });
    stopBad();
    stopGood();
    expect(forked).toBe(true);
    // One at a time: never two lineages for one tab.
    expect(calls.length).toBe(2);
    expect(getStoredRefreshToken()).toBe('forked-refresh');
  });

  test('all siblings refusing ends signed out once the timeout passes', async () => {
    stubFork(rejected);
    const stop = startForkResponder(() => 'stale');
    expect(await forkFromSibling({ timeoutMs: 60 })).toBe(false);
    stop();
    expect(getStoredToken()).toBeNull();
  });

  test('ignores offers answering another request and malformed payloads', async () => {
    const calls = stubFork(ok);
    const stranger = new FakeBroadcastChannel(`${FORK_CHANNEL_PREFIX}:auth_token`);
    const pending = forkFromSibling({ timeoutMs: 60 });
    stranger.postMessage({ kind: 'fork_offer', id: 'someone-elses', accessToken: 'x' });
    stranger.postMessage({ kind: 'fork_offer', accessToken: 'no-id' });
    stranger.postMessage('garbage');
    stranger.postMessage(null);
    expect(await pending).toBe(false);
    expect(calls.length).toBe(0);
    stranger.close();
  });

  test('a differently configured token key uses a separate channel', async () => {
    configureUsersApi({ tokenStorageKey: 'other_key' });
    const calls = stubFork(ok);
    // Responder started under the default key, requester under other_key.
    resetUsersApiConfig();
    const stop = startForkResponder(() => 'sibling-access');
    configureUsersApi({ tokenStorageKey: 'other_key' });
    expect(await forkFromSibling({ timeoutMs: 40 })).toBe(false);
    stop();
    expect(calls.length).toBe(0);
  });
});

describe('without BroadcastChannel', () => {
  test('fork resolves false and the responder is a no-op', async () => {
    (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = undefined;
    expect(await forkFromSibling({ timeoutMs: 10 })).toBe(false);
    expect(() => startForkResponder(() => 'x')()).not.toThrow();
  });
});
