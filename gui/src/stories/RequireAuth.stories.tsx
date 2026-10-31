import type { Story } from '@ladle/react';
import { RequireAuth } from '../components/require-auth';
import { AuthProvider } from '../lib/auth-context';
import { SignedInStory, STORY_PERSONAL } from './story-support';

// RequireAuth requires an AuthProvider in scope.
// These stories exercise the loading, unauthenticated, and authenticated states.

export const Loading: Story = () => (
  <AuthProvider>
    {/* isLoading starts true until the provider finishes its /self fetch.
        In isolation (no API) the provider stays in loading indefinitely. */}
    <RequireAuth>
      <div className="p-4 text-sm">You are authenticated</div>
    </RequireAuth>
  </AuthProvider>
);

export const Unauthenticated: Story = () => {
  // Demonstrate the callback prop in isolation.
  const handleUnauthenticated = () => {
    console.log('[story] onUnauthenticated fired — would navigate to /auth/login');
  };
  return (
    <AuthProvider>
      <RequireAuth onUnauthenticated={handleUnauthenticated}>
        <div className="p-4 text-sm">Protected content</div>
      </RequireAuth>
    </AuthProvider>
  );
};

export const Authenticated: Story = () => (
  <SignedInStory profiles={[STORY_PERSONAL]}>
    <RequireAuth onUnauthenticated={() => console.log('[story] unexpected redirect')}>
      <div className="p-4 text-sm">Protected content</div>
    </RequireAuth>
  </SignedInStory>
);

// `onSelectionRequired` fires instead of `onUnauthenticated` when a sign-in
// found no landing profile; a profile lock renders `ProfileLockNotice` in
// place of the children. Both states need a live API response, so they are
// covered by the component tests rather than a static story.
