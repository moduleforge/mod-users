import type { Story } from '@ladle/react';
import { RegisterForm } from '../components/register-form';
import { AuthProvider } from '../lib/auth-context';

// RegisterForm requires an AuthProvider in scope (useAuth()). Submission
// will fail with a `network_error` in this story environment since there's
// no live API — that's expected, matching how RequireAuth's story never
// reaches a real backend either.

export const Default: Story = () => (
  <AuthProvider>
    <div className="w-full max-w-sm p-6">
      <RegisterForm />
    </div>
  </AuthProvider>
);

export const WithAppAndSelectionRouting: Story = () => (
  <AuthProvider>
    <div className="w-full max-w-sm p-6">
      <RegisterForm
        app="demo"
        returnPath="/welcome"
        onSuccess={(returnPath) => console.log('registered', returnPath)}
        onSelectionRequired={(selectProfilePath, returnPath) =>
          console.log('select a profile at', selectProfilePath, 'then resume', returnPath)
        }
      />
    </div>
  </AuthProvider>
);
