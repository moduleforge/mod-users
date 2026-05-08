'use client';

import React from 'react';
import {
  ClipboardList,
  LayoutGrid,
  LogIn,
  LogOut,
  Settings,
  User,
  Users,
} from 'lucide-react';
import { cn } from '../lib/utils';
import { useAuth } from '../lib/auth-context';
import { Button } from '@moduleforge/core-gui';

interface NavItem {
  label: string;
  href: string;
  icon: React.ReactNode;
  adminOnly?: boolean;
}

const navItems: NavItem[] = [
  { label: 'Profile', href: '/profile', icon: <User className="size-4" /> },
  {
    label: 'Users',
    href: '/admin/user-accounts',
    icon: <Users className="size-4" />,
    adminOnly: true,
  },
  {
    label: 'Audit',
    href: '/admin/audit',
    icon: <ClipboardList className="size-4" />,
    adminOnly: true,
  },
  {
    label: 'Apps',
    href: '/admin/apps',
    icon: <LayoutGrid className="size-4" />,
    adminOnly: true,
  },
  {
    label: 'OIDC Settings',
    href: '/oidc-config',
    icon: <Settings className="size-4" />,
    adminOnly: true,
  },
];

export interface SidebarNavProps {
  /**
   * The current pathname, used to highlight the active nav item.
   * Inject from the router (e.g. Next.js `usePathname()`, React Router
   * `useLocation().pathname`).
   */
  currentPath: string;
  /**
   * A React component used to render navigation links. Must accept
   * `href`, `className`, and `children`. Pass Next.js `Link`, React Router
   * `NavLink`, or a plain `<a>` wrapper.
   */
  LinkComponent: React.ComponentType<{
    href: string;
    className?: string;
    children: React.ReactNode;
  }>;
}

export function SidebarNav({ currentPath, LinkComponent }: SidebarNavProps) {
  const { user, isAdmin, logout } = useAuth();

  return (
    <aside className="flex h-full w-56 flex-col border-r bg-sidebar">
      <div className="px-4 py-5 border-b">
        <h1 className="text-base font-semibold text-sidebar-foreground">User Manager</h1>
        {user && (
          <p className="mt-0.5 text-xs text-muted-foreground truncate">
            {user.given_name} {user.family_name}
          </p>
        )}
      </div>

      <nav className="flex flex-1 flex-col gap-1 p-2">
        {!user && (
          <LinkComponent
            href="/auth/login"
            className={cn(
              'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors',
              currentPath === '/auth/login'
                ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                : 'text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
            )}
          >
            <LogIn className="size-4" />
            Login
          </LinkComponent>
        )}

        {navItems
          .filter((item) => !item.adminOnly || isAdmin)
          .map((item) => (
            <LinkComponent
              key={item.href}
              href={item.href}
              className={cn(
                'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors',
                currentPath === item.href || currentPath.startsWith(item.href + '/')
                  ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                  : 'text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
              )}
            >
              {item.icon}
              {item.label}
            </LinkComponent>
          ))}
      </nav>

      {user && (
        <div className="border-t p-2">
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start gap-2.5 text-muted-foreground hover:text-foreground"
            onClick={logout}
          >
            <LogOut className="size-4" />
            Sign out
          </Button>
        </div>
      )}
    </aside>
  );
}
