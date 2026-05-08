import type { Story } from '@ladle/react';
import { SidebarNav } from '../components/sidebar-nav';
import { AuthProvider } from '../lib/auth-context';
import React from 'react';

// A minimal link component for story isolation — no router required.
function AnchorLink({
  href,
  className,
  children,
}: {
  href: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <a href={href} className={className} onClick={(e) => e.preventDefault()}>
      {children}
    </a>
  );
}

export const Guest: Story = () => (
  <AuthProvider>
    <div className="h-screen w-56">
      <SidebarNav currentPath="/auth/login" LinkComponent={AnchorLink} />
    </div>
  </AuthProvider>
);

export const OnProfilePage: Story = () => (
  <AuthProvider>
    <div className="h-screen w-56">
      <SidebarNav currentPath="/profile" LinkComponent={AnchorLink} />
    </div>
  </AuthProvider>
);

export const OnAdminPage: Story = () => (
  <AuthProvider>
    <div className="h-screen w-56">
      <SidebarNav currentPath="/admin/user-accounts" LinkComponent={AnchorLink} />
    </div>
  </AuthProvider>
);
