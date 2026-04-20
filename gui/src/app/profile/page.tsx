'use client';

import { ProfileEditor } from '@moduleforge/core-gui';
import { useAuth } from '@/lib/auth-context';
import { api } from '@/lib/api';
import { RequireAuth } from '@/components/require-auth';

function ProfileContent() {
  const { user, refreshUser } = useAuth();
  if (!user) return null;
  return (
    <ProfileEditor
      initial={{
        email: user.email,
        given_name: user.given_name ?? '',
        family_name: user.family_name ?? '',
        is_admin: user.is_admin,
        is_email_verified: false,
        created_at: user.created_at,
      }}
      onSave={async (patch) => {
        await api.self.update(patch);
        await refreshUser();
      }}
    />
  );
}

export default function ProfilePage() {
  return (
    <RequireAuth>
      <ProfileContent />
    </RequireAuth>
  );
}
