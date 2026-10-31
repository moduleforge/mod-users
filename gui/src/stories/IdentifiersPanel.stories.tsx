import type { Story } from '@ladle/react';
import { IdentifiersPanel } from '../components/identifiers-panel';
import { SignedInStory, STORY_PERSONAL } from './story-support';

const PRIMARY = {
  uuid: 'id-1',
  kind: 'email',
  value: 'ada@example.test',
  is_primary: true,
  verified: true,
  verified_at: '2026-01-01T00:00:00Z',
  created_at: '2026-01-01T00:00:00Z',
};
const UNVERIFIED = {
  uuid: 'id-2',
  kind: 'email',
  value: 'ada@work.example',
  is_primary: false,
  verified: false,
  verified_at: null,
  created_at: '2026-02-01T00:00:00Z',
};
const VERIFIED = { ...UNVERIFIED, uuid: 'id-3', value: 'ada@home.example', verified: true };

export const PrimaryVerifiedAndUnverified: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL]}
    routes={{
      'GET /v1/me/identifiers': () => ({ identifiers: [PRIMARY, VERIFIED, UNVERIFIED] }),
    }}
  >
    <IdentifiersPanel />
  </SignedInStory>
);

export const Empty: Story = () => (
  <SignedInStory profiles={[STORY_PERSONAL]} routes={{ 'GET /v1/me/identifiers': () => ({ identifiers: [] }) }}>
    <IdentifiersPanel />
  </SignedInStory>
);
