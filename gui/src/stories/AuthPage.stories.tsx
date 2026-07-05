import type { Story } from '@ladle/react';
import { AuthPage } from '../components/auth-page';
import { AuthProvider } from '../lib/auth-context';

// AuthPage requires an AuthProvider in scope (LoginForm/RegisterForm both
// call useAuth()). Submission will fail with a `network_error` in this story
// environment since there's no live API — that's expected, matching how
// RequireAuth's story never reaches a real backend either.

export const Default: Story = () => (
  <AuthProvider>
    <AuthPage />
  </AuthProvider>
);

export const RegisterMode: Story = () => (
  <AuthProvider>
    <AuthPage initialMode="register" />
  </AuthProvider>
);
