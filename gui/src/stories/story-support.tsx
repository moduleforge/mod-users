// Story-only helper: mounts children under a real AuthProvider whose tab is
// signed in against a scripted in-page "server", so the profile chrome renders
// with data and its actions answer, without a running API.

import React, { useState } from 'react';
import { AuthProvider } from '../lib/auth-context';
import {
  setStoredProfileUuid,
  setStoredRefreshToken,
  setStoredToken,
} from '../lib/config';
import type { ProfileView, SessionView } from '../lib/api';

export const STORY_PERSONAL: ProfileView = {
  uuid: 'story-personal',
  kind: 'personal',
  party: null,
  status: 'active',
  display_name: 'Ada Lovelace',
  available: true,
  lock: null,
};

export const STORY_PARTY: ProfileView = {
  uuid: 'story-acme',
  kind: 'party',
  party: { uuid: 'party-acme', kind: 'organization', display_name: 'Acme' },
  status: 'active',
  display_name: 'Ada Lovelace @ Acme',
  available: true,
  lock: null,
};

export const STORY_LOCKED_PROVIDER: ProfileView = {
  uuid: 'story-globex',
  kind: 'party',
  party: { uuid: 'party-globex', kind: 'organization', display_name: 'Globex' },
  status: 'active',
  display_name: 'Ada Lovelace @ Globex',
  available: false,
  lock: {
    reason: 'provider_required',
    remedy: { kind: 'sign_in_with_provider', provider_id: 'google' },
  },
};

export const STORY_LOCKED_STEP_UP: ProfileView = {
  uuid: 'story-initech',
  kind: 'party',
  party: { uuid: 'party-initech', kind: 'organization', display_name: 'Initech' },
  status: 'active',
  display_name: 'Ada Lovelace @ Initech',
  available: false,
  lock: { reason: 'acr_too_low', remedy: { kind: 'step_up' } },
};

export const STORY_INVITED: ProfileView = {
  uuid: 'story-umbrella',
  kind: 'party',
  party: { uuid: 'party-umbrella', kind: 'organization', display_name: 'Umbrella' },
  status: 'invited',
  display_name: 'Ada Lovelace @ Umbrella',
  available: false,
  lock: { reason: 'not_held', remedy: { kind: 'accept_invitation' } },
};

/** What a scripted story route answers: a ready Response, a JSON body, or nothing (204). */
export type StoryReply = Response | object | undefined;

export interface SignedInStoryProps {
  profiles: ProfileView[];
  active?: ProfileView;
  session?: Partial<SessionView>;
  /** Overrides of the `GET /v1/me` record (for example `{ is_guest: true }`). */
  user?: Record<string, unknown>;
  /**
   * Extra scripted routes keyed by "METHOD /path", consulted after the session
   * and user defaults. A route may return a different answer each call, so a
   * story can show a 409 first and a success after the user acts.
   */
  routes?: Record<string, (body: unknown) => StoryReply>;
  children: React.ReactNode;
}

/** A JSON error reply for story routes. */
export function storyError(
  status: number,
  code: string,
  message: string,
  details?: { field: string; code: string; message: string }[],
): Response {
  return new Response(JSON.stringify({ error: { code, message, details } }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** The step-up action-required reply (`409 users.step_up_required`). */
export function storyStepUpRequired(): Response {
  return new Response(
    JSON.stringify({
      action: { code: 'users.step_up_required', message: 'Confirm it is you.', path: '/step-up' },
    }),
    { status: 409, headers: { 'Content-Type': 'application/json' } },
  );
}

/** Installs the scripted fetch once, before the provider's first request. */
export function SignedInStory({
  profiles,
  active = STORY_PERSONAL,
  session,
  user,
  routes,
  children,
}: SignedInStoryProps) {
  // Lazy state initializer: runs once, before the first child effect, which is
  // when the provider hydrates from the stored tokens and calls the API.
  useState(() => {
    setStoredToken('story-access');
    setStoredRefreshToken('story-refresh');
    setStoredProfileUuid(active.uuid);
    const json = (body: unknown): Response =>
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    globalThis.fetch = (async (url: string, init: RequestInit = {}) => {
      const path = String(url).replace(/^https?:\/\/[^/]+/, '');
      const method = init.method ?? 'GET';
      if (method === 'GET' && path === '/v1/session') {
        return json({
          session: {
            uuid: 'story-session',
            acr: 1,
            amr: ['pwd'],
            auth_time: new Date().toISOString(),
            impersonating: false,
            ...session,
          },
          profile: active,
          profiles,
        });
      }
      if (method === 'GET' && path === '/v1/me') {
        return json({
          uuid: 'story-user',
          kind: 'user',
          is_guest: false,
          managed: false,
          given_name: 'Ada',
          family_name: 'Lovelace',
          display_name: 'Ada Lovelace',
          email: 'ada@example.test',
          email_verified: true,
          default_profile_uuid: null,
          default_app_uuid: null,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-01T00:00:00Z',
          ...user,
        });
      }
      if (method === 'GET' && path === '/v1/me/profiles') return json({ profiles });
      const route = routes?.[`${method} ${path}`];
      if (route) {
        // A scripted route: stories that need more than the session chrome
        // supply their own answers, so the panels render with data.
        const body = typeof init.body === 'string' ? JSON.parse(init.body) : undefined;
        const reply = route(body);
        if (reply instanceof Response) return reply;
        if (reply === undefined) return new Response(null, { status: 204 });
        return json(reply);
      }
      // Every other action answers 404: the stories show presentation, not
      // server behavior.
      return new Response(
        JSON.stringify({ error: { code: 'not_found', message: 'Not available in this story.' } }),
        { status: 404, headers: { 'Content-Type': 'application/json' } },
      );
    }) as unknown as typeof fetch;
    return null;
  });
  return <AuthProvider>{children}</AuthProvider>;
}
