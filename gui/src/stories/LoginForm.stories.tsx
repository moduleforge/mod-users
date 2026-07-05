import type { Story } from '@ladle/react';
import { LoginForm } from '../components/login-form';
import { AuthProvider } from '../lib/auth-context';

// LoginForm requires an AuthProvider in scope (useAuth()). Submission will
// fail with a `network_error` in this story environment since there's no
// live API — that's expected, matching how RequireAuth's story never
// reaches a real backend either.

export const Default: Story = () => (
  <AuthProvider>
    <div className="w-full max-w-sm p-6">
      <LoginForm />
    </div>
  </AuthProvider>
);

export const WithInitialError: Story = () => (
  <AuthProvider>
    <div className="w-full max-w-sm p-6">
      <LoginForm initialError="Invalid credentials. Please try again." />
    </div>
  </AuthProvider>
);
