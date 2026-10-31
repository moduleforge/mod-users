import type { Story } from '@ladle/react';
import { CredentialsPanel } from '../components/credentials-panel';
import { StepUpDialog } from '../components/step-up-dialog';
import {
  SignedInStory,
  STORY_PARTY,
  STORY_PERSONAL,
  storyError,
  storyStepUpRequired,
} from './story-support';

const BASE = {
  created_at: '2026-01-01T00:00:00Z',
  last_used_at: '2026-03-01T00:00:00Z',
  expires_at: null,
  default_profile_uuid: null,
  confined_to_party_uuid: null,
};

const PASSWORD = { ...BASE, uuid: 'cred-pw', kind: 'password', label: 'My password' };
const SSH_KEY = {
  ...BASE,
  uuid: 'cred-ssh',
  kind: 'ssh_key',
  label: 'Work laptop',
  key_type: 'ssh-ed25519',
  fingerprint: 'SHA256:3b0c5e2f8f0a1d4c7a9b6e5d4c3b2a1f0e9d8c7b6a5',
  public_key: 'ssh-ed25519 AAAA',
};
const OIDC = {
  ...BASE,
  uuid: 'cred-oidc',
  kind: 'oidc',
  label: '',
  provider_id: 'google',
  email_at_link: 'ada@gmail.example',
};

const PROFILES = [STORY_PERSONAL, STORY_PARTY];

export const WithCredentials: Story = () => (
  <SignedInStory
    profiles={PROFILES}
    routes={{
      'GET /v1/me/credentials': () => ({ credentials: [PASSWORD, SSH_KEY, OIDC] }),
      'GET /v1/auth/providers': () => [{ id: 'google', display_name: 'Google' }],
    }}
  >
    <CredentialsPanel />
  </SignedInStory>
);

export const Empty: Story = () => (
  <SignedInStory profiles={PROFILES} routes={{ 'GET /v1/me/credentials': () => ({ credentials: [] }) }}>
    <CredentialsPanel />
  </SignedInStory>
);

/** Revoking the only credential answers `409 users.last_credential`. */
export const LastCredentialError: Story = () => (
  <SignedInStory
    profiles={PROFILES}
    routes={{
      'GET /v1/me/credentials': () => ({ credentials: [PASSWORD] }),
      'DELETE /v1/me/credentials/cred-pw': () =>
        storyError(409, 'conflict', 'Conflict', [
          {
            field: 'credential',
            code: 'users.last_credential',
            message: 'the last sign-in credential cannot be removed',
          },
        ]),
    }}
  >
    <CredentialsPanel />
  </SignedInStory>
);

/** Revoking asks for a step-up first; the prompt then lets the revoke through. */
export const RevokeNeedsStepUp: Story = () => {
  let unlocked = false;
  return (
    <SignedInStory
      profiles={PROFILES}
      routes={{
        'GET /v1/me/credentials': () => ({ credentials: [PASSWORD, SSH_KEY] }),
        'DELETE /v1/me/credentials/cred-ssh': () => (unlocked ? undefined : storyStepUpRequired()),
        'POST /v1/session/step-up/password': () => {
          unlocked = true;
          return {
            access_token: 'story-fresh',
            token_type: 'Bearer',
            expires_in: 3600,
            refresh_token: null,
            session: {
              uuid: 'story-session',
              acr: 2,
              amr: ['pwd'],
              auth_time: new Date().toISOString(),
              impersonating: false,
            },
            profile: STORY_PERSONAL,
            selection_required: false,
            profiles: PROFILES,
          };
        },
      }}
    >
      <StepUpDialog providers={[]} />
      <CredentialsPanel />
    </SignedInStory>
  );
};

/** A guest cannot add credentials: the upgrade form replaces the add forms. */
export const GuestUpgrade: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL]}
    user={{ is_guest: true, email: null, email_verified: false, display_name: 'Guest' }}
    routes={{ 'GET /v1/me/credentials': () => ({ credentials: [] }) }}
  >
    <CredentialsPanel />
  </SignedInStory>
);
