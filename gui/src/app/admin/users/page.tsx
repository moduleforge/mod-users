'use client';

import { useEffect, useState, useCallback } from 'react';
import Link from 'next/link';
import { api, ApiRequestError, type User } from '@/lib/api';
import { RequireAuth } from '@/components/require-auth';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ErrorMessage } from '@/components/error-message';
import { Search } from 'lucide-react';
import { Input, Badge } from '@moduleforge/core-gui';

function UserListContent() {
  const [users, setUsers] = useState<User[]>([]);
  const [query, setQuery] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const loadUsers = useCallback(async (q: string) => {
    setIsLoading(true);
    setError(null);
    try {
      const response = await api.users.list(q || undefined);
      setUsers(response.users ?? []);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        setError('Failed to load users.');
      }
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadUsers('');
  }, [loadUsers]);

  function handleSearch(e: React.FormEvent) {
    e.preventDefault();
    void loadUsers(query);
  }

  return (
    <div className="p-6">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Users</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Manage user accounts
          </p>
        </div>
      </div>

      <form onSubmit={handleSearch} className="mb-4 flex gap-2 max-w-sm">
        <div className="relative flex-1">
          <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="text"
            placeholder="Search by name or email..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="pl-8"
          />
        </div>
      </form>

      <ErrorMessage message={error} />

      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={4} className="text-center text-muted-foreground py-8">
                    No users found.
                  </TableCell>
                </TableRow>
              ) : (
                users.map((user) => (
                  <TableRow key={user.uuid}>
                    <TableCell>
                      <Link
                        href={`/admin/users/${user.uuid}`}
                        className="font-medium hover:underline"
                      >
                        {user.given_name} {user.family_name}
                      </Link>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {user.email}
                    </TableCell>
                    <TableCell>
                      {user.is_admin ? (
                        <Badge>Admin</Badge>
                      ) : (
                        <Badge variant="secondary">User</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">
                      {new Date(user.created_at).toLocaleDateString()}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}

export default function AdminUsersPage() {
  return (
    <RequireAuth requireAdmin>
      <UserListContent />
    </RequireAuth>
  );
}
