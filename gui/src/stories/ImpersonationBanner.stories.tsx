import type { Story } from '@ladle/react';
import { ImpersonationBanner } from '../components/impersonation-banner';
import { SignedInStory, STORY_PARTY } from './story-support';

// After a reload the deadline is unknown, so the banner shows "Time remaining
// unknown" until a renew arrives.
export const Impersonating: Story = () => (
  <SignedInStory profiles={[STORY_PARTY]} active={STORY_PARTY} session={{ impersonating: true }}>
    <ImpersonationBanner />
  </SignedInStory>
);

export const NotImpersonating: Story = () => (
  <SignedInStory profiles={[STORY_PARTY]} active={STORY_PARTY}>
    <ImpersonationBanner />
  </SignedInStory>
);
