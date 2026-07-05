import type { Story } from '@ladle/react';
import { ResetPasswordPage } from '../components/reset-password-page';

// ResetPasswordPage does not call useAuth(), so no AuthProvider wrapper is
// needed. Submission will fail with a `network_error` in this story
// environment since there's no live API — that's expected, matching how
// LoginForm/RegisterForm's stories behave without a real backend.

export const Default: Story = () => <ResetPasswordPage token="dummy-reset-token" />;
