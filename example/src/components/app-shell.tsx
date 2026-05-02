'use client';

import { ClientLayout } from '@moduleforge/users-gui';
import { usePathname, useRouter } from 'next/navigation';
import Link from 'next/link';

/**
 * Client boundary that injects Next.js router + Link into the library's
 * ClientLayout. Kept separate from the server-component RootLayout so we can
 * export `metadata` from `layout.tsx` (not allowed in 'use client' files).
 */
export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();

  return (
    <ClientLayout
      currentPath={pathname}
      onNavigateToConfig={() => router.replace('/oidc-config')}
      onNavigate={(path) => router.push(path)}
      LinkComponent={Link}
    >
      {children}
    </ClientLayout>
  );
}
