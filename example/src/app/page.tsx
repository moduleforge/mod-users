'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useAuth } from '@moduleforge/users-gui';

export default function RootPage() {
  const { user, isLoading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isLoading) return;
    if (user) {
      router.replace('/profile');
    } else {
      router.replace('/auth/login');
    }
  }, [user, isLoading, router]);

  return null;
}
