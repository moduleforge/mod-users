'use client';

import { useRouter } from 'next/navigation';
import { RequireAuth as LibRequireAuth } from '@moduleforge/users-gui';

interface RequireAuthProps {
  children: React.ReactNode;
  requireAdmin?: boolean;
}

/**
 * Next.js-aware RequireAuth wrapper. Injects `useRouter` navigation
 * callbacks into the library's `RequireAuth` component so the library
 * stays framework-independent.
 */
export function RequireAuth({ children, requireAdmin = false }: RequireAuthProps) {
  const router = useRouter();
  return (
    <LibRequireAuth
      requireAdmin={requireAdmin}
      onUnauthenticated={() => router.replace('/auth/login')}
      onUnauthorized={() => router.replace('/profile')}
    >
      {children}
    </LibRequireAuth>
  );
}
