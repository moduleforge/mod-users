// Shared harness for the profile-chrome component tests: a scripted `fetch`
// and a signed-in `AuthProvider` mount over the real API client, so the
// components are exercised end to end (request out, token response in).

import { jwtFixture } from './jwt';
import { waitFor } from '@testing-library/react';
import { render } from '@testing-library/react';
import React from 'react';
import { AuthProvider, useAuth, type AuthContextValue } from '../lib/auth-context';
import {
  resetUsersApiConfig,
  setStoredProfileUuid,
  setStoredRefreshToken,
  setStoredToken,
} from '../lib/config';
import { setStepUpHandler } from '../lib/api';
import { tokenResponse, USER_VIEW, PROFILE_A } from './users-fixtures';
import type { ProfileView, SessionView } from '../lib/api';

export interface Call {
  method: string;
  path: string;
  body: unknown;
}

type RouteReply = Response | object | undefined;
type Route = (call: Call) => RouteReply | Promise<RouteReply>;

export const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

export const errorReply = (status: number, code: string, message = `${code} message`): Response =>
  jsonResponse({ error: { code, message } }, status);

const originalFetch = globalThis.fetch;
const realBroadcastChannel = (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel;

/** Call in `beforeEach`: no sibling channel, so a tokenless tab settles at once. */
export function setUpHarness(): void {
  (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = undefined;
}

/** Call in `afterEach`. */
export function tearDownHarness(): void {
  globalThis.fetch = originalFetch;
  (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = realBroadcastChannel;
  resetUsersApiConfig();
  setStepUpHandler(null);
  sessionStorage.clear();
}

/** Scripts fetch by "METHOD /path"; unscripted routes answer 404. */
export function mockApi(routes: Record<string, Route>): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (url: string, init: RequestInit = {}) => {
    const path = String(url).replace('http://localhost:8080', '');
    const call: Call = {
      method: init.method ?? 'GET',
      path,
      body: typeof init.body === 'string' ? JSON.parse(init.body) : undefined,
    };
    calls.push(call);
    const route = routes[`${call.method} ${path}`];
    if (!route) return errorReply(404, 'not_found');
    const reply = await route(call);
    if (reply instanceof Response) return reply;
    if (reply === undefined) return new Response(null, { status: 204 });
    return jsonResponse(reply);
  }) as unknown as typeof fetch;
  return calls;
}

export interface SignedInOptions {
  profiles: ProfileView[];
  active?: ProfileView;
  session?: Partial<SessionView>;
  /** Extra routes merged over the session/me defaults. */
  routes?: Record<string, Route>;
}

export interface Mounted {
  auth: () => AuthContextValue;
  calls: Call[];
  navigations: string[];
}

/**
 * Mounts `ui` under a real `AuthProvider` whose tab already holds tokens, with
 * `GET /v1/session` and `GET /v1/me` scripted, and waits for hydration.
 */
export async function mountSignedIn(
  ui: React.ReactNode,
  options: SignedInOptions,
): Promise<Mounted> {
  setStoredToken(jwtFixture('old-access'));
  setStoredRefreshToken('old-refresh');
  setStoredProfileUuid('prof-a');
  const base = tokenResponse();
  const calls = mockApi({
    'GET /v1/session': () => ({
      session: { ...base.session, ...options.session },
      profile: options.active ?? PROFILE_A,
      profiles: options.profiles,
    }),
    'GET /v1/me': () => USER_VIEW,
    ...options.routes,
  });
  return mountUnder(ui, calls, () => true);
}

/** Mounts `ui` under an `AuthProvider` over whatever routes were scripted. */
export async function mountUnder(
  ui: React.ReactNode,
  calls: Call[],
  ready: (auth: AuthContextValue) => boolean,
): Promise<Mounted> {
  let latest: AuthContextValue | null = null;
  const navigations: string[] = [];
  function Capture() {
    latest = useAuth();
    return null;
  }
  render(
    <AuthProvider onNavigate={(p) => navigations.push(p)}>
      <Capture />
      {ui}
    </AuthProvider>,
  );
  const auth = (): AuthContextValue => {
    if (!latest) throw new Error('provider has not rendered');
    return latest;
  };
  await waitFor(() => {
    if (auth().isLoading || !ready(auth())) throw new Error('not ready');
  });
  return { auth, calls, navigations };
}
