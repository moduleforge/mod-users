import type { Story } from '@ladle/react';
import { EmailCodePage } from '../components/email-code-page';
import { AuthProvider } from '../lib/auth-context';

// EmailCodePage requires an AuthProvider in scope (useAuth()). Submission
// will fail with a `network_error` in this story environment since there's
// no live API — that's expected, matching how LoginForm's story never
// reaches a real backend either.

export const Default: Story = () => (
  <AuthProvider>
    <div className="w-full max-w-sm p-6">
      <EmailCodePage />
    </div>
  </AuthProvider>
);

export const WithAppAndSelectionRouting: Story = () => (
  <AuthProvider>
    <div className="w-full max-w-sm p-6">
      <EmailCodePage
        app="demo"
        onSuccess={(returnPath) => console.log('signed in', returnPath)}
        onSelectionRequired={(selectProfilePath) => console.log('select a profile at', selectProfilePath)}
      />
    </div>
  </AuthProvider>
);
