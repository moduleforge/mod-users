import type { Story } from '@ladle/react';
import { SessionsPanel } from '../components/sessions-panel';
import { SignedInStory, STORY_PERSONAL } from './story-support';

const BASE = {
  acr: 1,
  amr: ['pwd'],
  auth_time: '2026-03-01T09:00:00Z',
  created_at: '2026-03-01T09:00:00Z',
  last_seen_at: '2026-03-02T10:30:00Z',
  expires_at: '2026-04-01T09:00:00Z',
  impersonating: false,
};

export const CurrentAndOthers: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL]}
    routes={{
      'GET /v1/me/sessions': () => ({
        sessions: [
          { ...BASE, uuid: 'sess-1', current: true },
          { ...BASE, uuid: 'sess-2', amr: ['pwd', 'otp'], acr: 2, current: false },
          { ...BASE, uuid: 'sess-3', amr: ['oidc'], current: false },
        ],
      }),
    }}
  >
    <SessionsPanel />
  </SignedInStory>
);
