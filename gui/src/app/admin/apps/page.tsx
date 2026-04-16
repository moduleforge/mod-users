'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { api, ApiRequestError, type App } from '@/lib/api';
import { RequireAuth } from '@/components/require-auth';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ErrorMessage } from '@/components/error-message';
import { Plus, X } from 'lucide-react';

function AppsContent() {
  const [apps, setApps] = useState<App[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDescription, setNewDescription] = useState('');
  const [isCreating, setIsCreating] = useState(false);

  const loadApps = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const response = await api.apps.list();
      setApps(response.apps ?? []);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        setError('Failed to load apps.');
      }
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadApps();
  }, [loadApps]);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    setIsCreating(true);
    try {
      const app = await api.apps.create({
        name: newName,
        description: newDescription || undefined,
      });
      setApps((prev) => [app, ...prev]);
      setNewName('');
      setNewDescription('');
      setShowForm(false);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        setFormError('Failed to create app.');
      }
    } finally {
      setIsCreating(false);
    }
  }

  return (
    <div className="p-6">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Apps</h1>
          <p className="text-sm text-muted-foreground mt-1">Manage applications</p>
        </div>
        <Button onClick={() => setShowForm((v) => !v)} size="sm">
          {showForm ? (
            <>
              <X className="size-4" />
              Cancel
            </>
          ) : (
            <>
              <Plus className="size-4" />
              New app
            </>
          )}
        </Button>
      </div>

      {showForm && (
        <Card className="mb-4 max-w-md">
          <CardHeader>
            <CardTitle>Create new app</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleCreate} className="flex flex-col gap-4">
              <ErrorMessage message={formError} />
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="app-name">Name</Label>
                <Input
                  id="app-name"
                  type="text"
                  required
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="My App"
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="app-description">Description</Label>
                <Input
                  id="app-description"
                  type="text"
                  value={newDescription}
                  onChange={(e) => setNewDescription(e.target.value)}
                  placeholder="Optional description"
                />
              </div>
              <div className="flex justify-end">
                <Button type="submit" disabled={isCreating}>
                  {isCreating ? 'Creating...' : 'Create app'}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      <ErrorMessage message={error} />

      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {apps.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={3} className="text-center text-muted-foreground py-8">
                    No apps yet.
                  </TableCell>
                </TableRow>
              ) : (
                apps.map((app) => (
                  <TableRow key={app.uuid}>
                    <TableCell>
                      <Link
                        href={`/admin/apps/${app.uuid}`}
                        className="font-medium hover:underline"
                      >
                        {app.name}
                      </Link>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-sm">
                      {app.description ?? '—'}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {new Date(app.created_at).toLocaleDateString()}
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

export default function AdminAppsPage() {
  return (
    <RequireAuth requireAdmin>
      <AppsContent />
    </RequireAuth>
  );
}
