import type { Story } from '@ladle/react';
import { VerifyEmailPage } from '../components/verify-email-page';
import { AuthProvider } from '../lib/auth-context';

// Submission fails with a `network_error` in this story environment since
// there is no live API — that's expected, as for the other page stories.

/** Default: the address is known (passed as a prop). */
export const Default: Story = () => (
  <AuthProvider>
    <VerifyEmailPage email="you@example.com" onNavigateToLogin={() => {}} />
  </AuthProvider>
);

/** No session and no `email` prop: the page asks for the address first. */
export const NoSessionEmailField: Story = () => <VerifyEmailPage />;

/** Error state: submit any code to see the network-error banner. */
export const ErrorState: Story = () => (
  <VerifyEmailPage email="you@example.com" resendCooldownSeconds={0} />
);
