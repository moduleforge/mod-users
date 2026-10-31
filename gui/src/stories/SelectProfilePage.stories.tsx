import type { Story } from '@ladle/react';
import { SelectProfilePage } from '../components/select-profile-page';
import { AuthProvider } from '../lib/auth-context';

// `SelectProfilePage` renders only while the context reports
// `selectionRequired`, which a `selection_required` sign-in sets. With no API
// in a story the provider stays signed out, so this story shows the empty
// (hidden) state; the populated page is covered by the component tests.
export const NoSelectionRequired: Story = () => (
  <AuthProvider>
    <SelectProfilePage />
  </AuthProvider>
);
