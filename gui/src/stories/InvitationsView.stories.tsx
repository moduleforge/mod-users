import type { Story } from '@ladle/react';
import { InvitationsView } from '../components/invitations-view';
import { SignedInStory, STORY_INVITED, STORY_PARTY, STORY_PERSONAL } from './story-support';

export const PendingInvitationAndMembership: Story = () => (
  <SignedInStory profiles={[STORY_PERSONAL, STORY_PARTY, STORY_INVITED]}>
    <InvitationsView />
  </SignedInStory>
);

export const NoInvitations: Story = () => (
  <SignedInStory profiles={[STORY_PERSONAL]}>
    <InvitationsView />
  </SignedInStory>
);
