'use client';

import { use, useEffect, useState } from 'react';
import Link from 'next/link';
import { api, ApiRequestError, type App, type AppMember } from '@moduleforge/users-gui';
import { RequireAuth } from '@/components/require-auth';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@moduleforge/users-gui';
import { ErrorMessage } from '@moduleforge/users-gui';
import { ArrowLeft, Plus, Trash2, CheckCircle2 } from 'lucide-react';
import { Button, Input, Label, Card, CardContent, CardHeader, CardTitle } from '@moduleforge/core-gui';

function AppDetailContent({ uuid }: { uuid: string }) {
  const [app, setApp] = useState<App | null>(null);
  const [members, setMembers] = useState<AppMember[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editError, setEditError] = useState<string | null>(null);
  const [memberError, setMemberError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [isUpdating, setIsUpdating] = useState(false);
  const [updateSuccess, setUpdateSuccess] = useState(false);
  const [newMemberUuid, setNewMemberUuid] = useState('');
  const [newMemberRole, setNewMemberRole] = useState('member');
  const [isAddingMember, setIsAddingMember] = useState(false);

  useEffect(() => {
    async function load() {
      setIsLoading(true);
      try {
        const [appData, membersData] = await Promise.all([
          api.apps.get(uuid),
          api.apps.getMembers(uuid),
        ]);
        setApp(appData);
        setName(appData.name);
        setDescription(appData.description ?? '');
        setMembers(membersData.members ?? []);
      } catch (err) {
        if (err instanceof ApiRequestError) {
          setError(err.message);
        } else {
          setError('Failed to load app.');
        }
      } finally {
        setIsLoading(false);
      }
    }
    void load();
  }, [uuid]);

  async function handleUpdate(e: React.FormEvent) {
    e.preventDefault();
    setEditError(null);
    setUpdateSuccess(false);
    setIsUpdating(true);
    try {
      const updated = await api.apps.update(uuid, {
        name,
        description: description || undefined,
      });
      setApp(updated);
      setUpdateSuccess(true);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setEditError(err.message);
      } else {
        setEditError('Failed to update app.');
      }
    } finally {
      setIsUpdating(false);
    }
  }

  async function handleAddMember(e: React.FormEvent) {
    e.preventDefault();
    setMemberError(null);
    setIsAddingMember(true);
    try {
      await api.apps.addMember(uuid, {
        user_uuid: newMemberUuid.trim(),
        role: newMemberRole,
      });
      const membersData = await api.apps.getMembers(uuid);
      setMembers(membersData.members ?? []);
      setNewMemberUuid('');
      setNewMemberRole('member');
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setMemberError(err.message);
      } else {
        setMemberError('Failed to add member.');
      }
    } finally {
      setIsAddingMember(false);
    }
  }

  async function handleRemoveMember(userUuid: string) {
    setMemberError(null);
    try {
      await api.apps.removeMember(uuid, userUuid);
      setMembers((prev) => prev.filter((m) => m.user_uuid !== userUuid));
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setMemberError(err.message);
      } else {
        setMemberError('Failed to remove member.');
      }
    }
  }

  if (isLoading) {
    return <p className="p-6 text-sm text-muted-foreground">Loading...</p>;
  }

  if (!app) {
    return (
      <div className="p-6">
        <ErrorMessage message={error ?? 'App not found.'} />
      </div>
    );
  }

  return (
    <div className="p-6 max-w-2xl">
      <div className="mb-6">
        <Link
          href="/admin/apps"
          className="flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground mb-4"
        >
          <ArrowLeft className="size-4" />
          Back to apps
        </Link>
        <h1 className="text-2xl font-semibold">{app.name}</h1>
        {app.description && (
          <p className="text-sm text-muted-foreground mt-1">{app.description}</p>
        )}
      </div>

      <div className="flex flex-col gap-4">
        <Card>
          <CardHeader>
            <CardTitle>Edit app</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleUpdate} className="flex flex-col gap-4">
              <ErrorMessage message={editError} />
              {updateSuccess && (
                <div className="flex items-center gap-2 rounded-lg border border-green-200 bg-green-50 px-3 py-2 text-sm text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-200">
                  <CheckCircle2 className="size-4" />
                  App updated successfully.
                </div>
              )}
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="app-name">Name</Label>
                <Input
                  id="app-name"
                  type="text"
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="app-description">Description</Label>
                <Input
                  id="app-description"
                  type="text"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Optional description"
                />
              </div>
              <div className="flex justify-end">
                <Button type="submit" disabled={isUpdating}>
                  {isUpdating ? 'Saving...' : 'Save changes'}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Members</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <form onSubmit={handleAddMember} className="flex flex-col gap-3">
              <ErrorMessage message={memberError} />
              <div className="flex gap-2 items-end">
                <div className="flex-1 flex flex-col gap-1.5">
                  <Label htmlFor="member-uuid">User UUID</Label>
                  <Input
                    id="member-uuid"
                    type="text"
                    required
                    value={newMemberUuid}
                    onChange={(e) => setNewMemberUuid(e.target.value)}
                    placeholder="user-uuid-..."
                    className="font-mono text-sm"
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="member-role">Role</Label>
                  <Input
                    id="member-role"
                    type="text"
                    value={newMemberRole}
                    onChange={(e) => setNewMemberRole(e.target.value)}
                    placeholder="member"
                    className="w-28"
                  />
                </div>
                <Button type="submit" disabled={isAddingMember} size="sm">
                  <Plus className="size-4" />
                  Add
                </Button>
              </div>
            </form>

            {members.length === 0 ? (
              <p className="text-sm text-muted-foreground">No members yet.</p>
            ) : (
              <div className="rounded-lg border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Email</TableHead>
                      <TableHead>Role</TableHead>
                      <TableHead></TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {members.map((member) => (
                      <TableRow key={member.user_uuid}>
                        <TableCell className="font-medium">
                          {member.given_name} {member.family_name}
                        </TableCell>
                        <TableCell className="text-sm text-muted-foreground">
                          {member.email}
                        </TableCell>
                        <TableCell className="text-sm">
                          {member.role}
                        </TableCell>
                        <TableCell className="text-right">
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            onClick={() => handleRemoveMember(member.user_uuid)}
                            className="text-muted-foreground hover:text-destructive"
                          >
                            <Trash2 className="size-4" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

export default function AppDetailPage({
  params,
}: {
  params: Promise<{ uuid: string }>;
}) {
  const { uuid } = use(params);
  return (
    <RequireAuth requireAdmin>
      <AppDetailContent uuid={uuid} />
    </RequireAuth>
  );
}
