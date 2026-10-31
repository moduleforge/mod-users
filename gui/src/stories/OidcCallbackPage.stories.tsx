import type { Story } from '@ladle/react';
import { OidcCallbackPage } from '../components/oidc-callback-page';
import { AuthProvider } from '../lib/auth-context';

// The page reads its result from the URL fragment. Both stories set the
// fragment before the page mounts. With no live API (and no verifier stored by
// a sign-in this tab started), the handoff story ends on the "same browser tab"
// message; the refusal story shows the readable copy for `#error=<code>`.

function Callback() {
  return (
    <AuthProvider>
      <OidcCallbackPage
        onComplete={(returnPath) => console.log('complete', returnPath)}
        onError={(message) => console.log('error', message)}
        onSelectionRequired={(selectProfilePath, returnPath) =>
          console.log('select a profile at', selectProfilePath, 'then resume', returnPath)
        }
      />
    </AuthProvider>
  );
}

/** A sign-in redirect: `#handoff=<code>`. */
export const Handoff: Story = () => {
  window.location.hash = '#handoff=demo-code&return=%2Fdashboard';
  return <Callback />;
};

/** A refusal redirect: an account already exists for the email. */
export const AccountExists: Story = () => {
  window.location.hash = '#error=account_exists_sign_in_and_link';
  return <Callback />;
};
