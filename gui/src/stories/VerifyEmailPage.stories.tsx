import type { Story } from '@ladle/react';
import { VerifyEmailPage } from '../components/verify-email-page';
import { AuthProvider } from '../lib/auth-context';

// The page verifies through the signed-in user's identifier routes, so a
// request fails with a `network_error` in this story environment: there is no
// live API — that's expected, as for the other page stories.

/** Default: the address is known (passed as a prop). */
export const Default: Story = () => (
  <AuthProvider>
    <VerifyEmailPage email="you@example.com" onNavigateToLogin={() => {}} />
  </AuthProvider>
);

/** No `email` prop: the account's first unverified address is verified and shown. */
export const AddressFromAccount: Story = () => (
  <AuthProvider>
    <VerifyEmailPage />
  </AuthProvider>
);

/** Error state: submit any code to see the network-error banner. */
export const ErrorState: Story = () => (
  <VerifyEmailPage email="you@example.com" resendCooldownSeconds={0} />
);
