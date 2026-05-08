'use client';

import { Suspense, useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import { api, ApiRequestError, type AuditEntry } from '@moduleforge/users-gui';
import { RequireAuth } from '@/components/require-auth';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@moduleforge/users-gui';
import { ErrorMessage } from '@moduleforge/users-gui';
import { Search } from 'lucide-react';
import { Button, Input, Label } from '@moduleforge/core-gui';

function AuditContent() {
  const searchParams = useSearchParams();
  const initialUserUuid = searchParams.get('user_uuid') ?? '';
  const [entityUuid, setEntityUuid] = useState(initialUserUuid);
  const [inputValue, setInputValue] = useState(initialUserUuid);
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const loadAudit = useCallback(async (filterEntityUuid: string) => {
    setIsLoading(true);
    setError(null);
    try {
      const response = filterEntityUuid
        ? await api.audit.byEntity(filterEntityUuid)
        : await api.audit.list();
      setEntries(response.entries ?? []);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        setError('Failed to load audit log.');
      }
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadAudit(entityUuid);
  }, [loadAudit, entityUuid]);

  function handleSearch(e: React.FormEvent) {
    e.preventDefault();
    setEntityUuid(inputValue.trim());
  }

  function handleClear() {
    setInputValue('');
    setEntityUuid('');
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold">Audit Log</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Track all data changes in the system
        </p>
      </div>

      <form onSubmit={handleSearch} className="mb-4 flex flex-col gap-2 max-w-lg">
        <Label htmlFor="entity-uuid">Filter by entity UUID</Label>
        <div className="flex gap-2">
          <div className="relative flex-1">
            <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              id="entity-uuid"
              type="text"
              placeholder="e.g. abc-123-..."
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              className="pl-8 font-mono text-sm"
            />
          </div>
          <Button type="submit">Search</Button>
          {entityUuid && (
            <Button type="button" variant="outline" onClick={handleClear}>
              Clear
            </Button>
          )}
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
                <TableHead>When</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Entity type</TableHead>
                <TableHead>Entity UUID</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                    No audit entries found.
                  </TableCell>
                </TableRow>
              ) : (
                entries.map((entry) => (
                  <TableRow key={entry.id}>
                    <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
                      {new Date(entry.created_at).toLocaleString()}
                    </TableCell>
                    <TableCell className="text-sm">
                      <div>{entry.actor_email}</div>
                      <div className="text-xs text-muted-foreground font-mono">
                        {entry.actor_uuid}
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs font-medium">
                      {entry.action}
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {entry.entity_type}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {entry.entity_uuid}
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

export default function AdminAuditPage() {
  return (
    <RequireAuth requireAdmin>
      <Suspense fallback={<p className="p-6 text-sm text-muted-foreground">Loading...</p>}>
        <AuditContent />
      </Suspense>
    </RequireAuth>
  );
}
