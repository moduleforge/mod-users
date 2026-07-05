import type { Story } from '@ladle/react';
import { ForgotPasswordPage } from '../components/forgot-password-page';

// ForgotPasswordPage does not call useAuth(), so no AuthProvider is needed
// here (unlike LoginForm/RegisterForm/AuthPage's stories). Submission will
// fail with a `network_error` in this story environment since there's no
// live API — that's expected, matching how the other stories are documented.

export const Default: Story = () => <ForgotPasswordPage />;
