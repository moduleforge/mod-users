import type { Story } from '@ladle/react';
import { RequireAuth } from '../components/require-auth';
import { AuthProvider } from '../lib/auth-context';

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
