import type { Story } from '@ladle/react';
import { ProfileSwitcher } from '../components/profile-switcher';
import {
  SignedInStory,
  STORY_LOCKED_PROVIDER,
  STORY_LOCKED_STEP_UP,
  STORY_PARTY,
  STORY_PERSONAL,
} from './story-support';

export const PersonalAndParty: Story = () => (
  <SignedInStory profiles={[STORY_PERSONAL, STORY_PARTY]}>
    <div className="w-64 p-2">
      <ProfileSwitcher />
    </div>
  </SignedInStory>
);

export const WithLockedProfiles: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL, STORY_PARTY, STORY_LOCKED_PROVIDER, STORY_LOCKED_STEP_UP]}
  >
    <div className="w-64 p-2">
      <ProfileSwitcher />
    </div>
  </SignedInStory>
);
