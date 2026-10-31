// Shared fixtures for the lib tests: token responses and profile views shaped
// like the final API's `TokenResponse` / `ProfileView`.

import { jwtFixture } from './jwt';
import type { ProfileView, TokenResponse } from '../lib/api';

export const PROFILE_A: ProfileView = {
  uuid: 'prof-a',
  kind: 'personal',
  party: null,
  status: 'active',
  display_name: 'Ada',
  available: true,
  lock: null,
};

export const PROFILE_B: ProfileView = {
  uuid: 'prof-b',
  kind: 'party',
  party: { uuid: 'party-1', kind: 'organization', display_name: 'Acme' },
  status: 'active',
  display_name: 'Ada @ Acme',
  available: true,
  lock: null,
};

export function tokenResponse(overrides: Partial<TokenResponse> = {}): TokenResponse {
  return {
    access_token: jwtFixture('access-1'),
    token_type: 'Bearer',
    expires_in: 3600,
    refresh_token: 'refresh-1',
    session: {
      uuid: 'sess-1',
      acr: 1,
      amr: ['pwd'],
      auth_time: '2026-01-01T00:00:00Z',
      impersonating: false,
    },
    profile: PROFILE_A,
    selection_required: false,
    profiles: [PROFILE_A, PROFILE_B],
    ...overrides,
  };
}

export const USER_VIEW = {
  uuid: 'user-1',
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
};
