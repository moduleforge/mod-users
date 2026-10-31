import type { Story } from '@ladle/react';
import { GuestUpgradeForm } from '../components/guest-upgrade-form';
import { SignedInStory, STORY_PERSONAL, storyError } from './story-support';

const GUEST = { is_guest: true, email: null, email_verified: false, display_name: 'Guest' };

export const Upgrade: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL]}
    user={GUEST}
    routes={{ 'POST /v1/me/upgrade': () => ({ uuid: 'story-user', is_guest: false }) }}
  >
    <GuestUpgradeForm />
  </SignedInStory>
);

export const EmailAlreadyRegistered: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL]}
    user={GUEST}
    routes={{
      'POST /v1/me/upgrade': () =>
        storyError(409, 'conflict', 'Conflict', [
          { field: 'value', code: 'users.identifier_taken', message: 'this identifier is already registered' },
        ]),
    }}
  >
    <GuestUpgradeForm />
  </SignedInStory>
);
