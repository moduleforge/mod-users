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

export interface SignedInStoryProps {
  profiles: ProfileView[];
  active?: ProfileView;
  session?: Partial<SessionView>;
  children: React.ReactNode;
}

/** Installs the scripted fetch once, before the provider's first request. */
export function SignedInStory({
  profiles,
  active = STORY_PERSONAL,
  session,
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
        });
      }
      if (method === 'GET' && path === '/v1/me/profiles') return json({ profiles });
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
