import type { Story } from '@ladle/react';
import { SidebarNav } from '../components/sidebar-nav';
import { AuthProvider } from '../lib/auth-context';
import React from 'react';
import {
  SignedInStory,
  STORY_LOCKED_PROVIDER,
  STORY_PARTY,
  STORY_PERSONAL,
} from './story-support';

// A minimal link component for story isolation — no router required.
function AnchorLink({
  href,
  className,
  children,
}: {
  href: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <a href={href} className={className} onClick={(e) => e.preventDefault()}>
      {children}
    </a>
  );
}

export const Guest: Story = () => (
  <AuthProvider>
    <div className="h-screen w-56">
      <SidebarNav currentPath="/auth/login" LinkComponent={AnchorLink} />
    </div>
  </AuthProvider>
);

export const OnProfilePage: Story = () => (
  <AuthProvider>
    <div className="h-screen w-56">
      <SidebarNav currentPath="/profile" LinkComponent={AnchorLink} />
    </div>
  </AuthProvider>
);

export const OnAdminPage: Story = () => (
  <AuthProvider>
    <div className="h-screen w-56">
      <SidebarNav currentPath="/admin/user-accounts" LinkComponent={AnchorLink} />
    </div>
  </AuthProvider>
);

// Signed in: the profile switcher (with a locked profile and its remedy) and
// the Invitations entry appear; there is no admin filtering.
export const SignedInWithProfiles: Story = () => (
  <SignedInStory profiles={[STORY_PERSONAL, STORY_PARTY, STORY_LOCKED_PROVIDER]}>
    <div className="h-screen w-56">
      <SidebarNav currentPath="/invitations" LinkComponent={AnchorLink} />
    </div>
  </SignedInStory>
);
