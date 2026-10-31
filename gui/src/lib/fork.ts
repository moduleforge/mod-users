// Cross-tab fork: a tab that starts with no tokens asks its same-origin sibling
// tabs for an access token over a BroadcastChannel, then calls
// `POST /v1/session/fork` with it to get its OWN refresh lineage. Only access
// tokens ever cross the channel, never refresh tokens: a refresh token handed
// to a second tab would put two tabs on one lineage, and the second rotation
// would trip the server's reuse detection and revoke the whole session.
//
// BroadcastChannel is same-origin by specification, so only code running on
// this origin can ask for, or answer with, a token; that is the same trust
// boundary sessionStorage has. Where BroadcastChannel is undefined (tests,
// SSR, old browsers) both sides degrade to doing nothing.

import { api, type UsersClient } from './api';
import { getForkTimeoutMs, getTokenStorageKey } from './config';

/** Channel name prefix; the configured token key is appended. */
export const FORK_CHANNEL_PREFIX = 'moduleforge-users-gui:fork';

type ForkMessage =
  | { kind: 'fork_request'; id: string }
  | { kind: 'fork_offer'; id: string; accessToken: string };

/**
 * One channel per configured token key, so two embedded apps on one origin
 * with different `tokenStorageKey`s never exchange each other's tokens.
 */
function channelName(): string {
  return `${FORK_CHANNEL_PREFIX}:${getTokenStorageKey()}`;
}

/** Narrows an untrusted channel payload to a well-formed message, or null. */
function parseMessage(data: unknown): ForkMessage | null {
  if (typeof data !== 'object' || data === null) return null;
  const m = data as Record<string, unknown>;
  if (typeof m.id !== 'string' || m.id === '') return null;
  if (m.kind === 'fork_request') return { kind: 'fork_request', id: m.id };
  if (
    m.kind === 'fork_offer' &&
    typeof m.accessToken === 'string' &&
    m.accessToken !== ''
  ) {
    return { kind: 'fork_offer', id: m.id, accessToken: m.accessToken };
  }
  // Anything else (a foreign message on the channel) is ignored.
  return null;
}

function newRequestId(): string {
  const c = globalThis.crypto;
  // The id only pairs an offer with its request; it is not a secret.
  return typeof c?.randomUUID === 'function'
    ? c.randomUUID()
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

/**
 * Answers sibling tabs' fork requests with this tab's access token. Call once
 * while a signed-in provider is mounted; returns the stop function.
 *
 * `getAccessToken` returns the token to offer, or null to stay silent (no
 * session, or an impersonation, which the server refuses to fork).
 */
export function startForkResponder(
  getAccessToken: () => string | null,
): () => void {
  if (typeof BroadcastChannel === 'undefined') {
    // No channel support: this tab simply cannot help siblings sign in.
    return () => {};
  }
  const channel = new BroadcastChannel(channelName());
  channel.onmessage = (event: MessageEvent) => {
    const message = parseMessage(event.data);
    if (!message || message.kind !== 'fork_request') return;
    const accessToken = getAccessToken();
    // Silence, not a refusal: the asker is waiting for ANY sibling that has a
    // session, so a tab without one must not answer.
    if (!accessToken) return;
    channel.postMessage({
      kind: 'fork_offer',
      id: message.id,
      accessToken,
    } satisfies ForkMessage);
  };
  return () => channel.close();
}

export interface ForkOptions {
  /** Overrides the configured fork timeout (ms). */
  timeoutMs?: number;
  /** The client whose `session.fork` is called (default: the shared client). */
  client?: Pick<UsersClient, 'session'>;
}

/**
 * Asks sibling tabs for an access token and forks a lineage from the first one
 * the server accepts. The fork response is committed to this tab's storage by
 * the client. Resolves `true` when this tab now holds a session, `false` when
 * no sibling answered (or none answered with a token the server accepted)
 * within the timeout: the tab then stays signed out.
 */
export function forkFromSibling(options: ForkOptions = {}): Promise<boolean> {
  if (typeof BroadcastChannel === 'undefined') return Promise.resolve(false);
  const client = options.client ?? api;
  const timeoutMs = options.timeoutMs ?? getForkTimeoutMs();

  return new Promise<boolean>((resolve) => {
    const channel = new BroadcastChannel(channelName());
    const requestId = newRequestId();
    const offers: string[] = [];
    let settled = false;
    let busy = false;
    let timedOut = false;

    const finish = (forked: boolean): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      channel.close();
      resolve(forked);
    };

    // Offers are tried one at a time: two parallel forks would create two
    // lineages for one tab and orphan one of them.
    const tryNext = (): void => {
      if (busy || settled) return;
      const accessToken = offers.shift();
      if (accessToken === undefined) {
        // Nothing left to try; if the wait is also over, the answer is no.
        if (timedOut) finish(false);
        return;
      }
      busy = true;
      client.session.fork({ accessToken }).then(
        () => finish(true),
        () => {
          // This sibling's token was stale or refused (for example it is
          // impersonating); another sibling may still have a good one.
          busy = false;
          tryNext();
        },
      );
    };

    const timer = setTimeout(() => {
      timedOut = true;
      // An attempt still in flight decides the outcome when it settles.
      if (!busy && offers.length === 0) finish(false);
    }, timeoutMs);

    channel.onmessage = (event: MessageEvent) => {
      const message = parseMessage(event.data);
      // Ignore other tabs' traffic and offers answering someone else's request.
      if (!message || message.kind !== 'fork_offer') return;
      if (message.id !== requestId) return;
      offers.push(message.accessToken);
      tryNext();
    };

    channel.postMessage({
      kind: 'fork_request',
      id: requestId,
    } satisfies ForkMessage);
  });
}
