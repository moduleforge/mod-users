'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  AlertDescription,
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './ui/dialog';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table';
import { useAuth } from '../lib/auth-context';
import { api, type ApiActionRequiredError, type SelfSession } from '../lib/api';
import { describeFailure, formatDateTime } from '../lib/self-service';

const DEFAULT_DESCRIPTION =
  'Where you are signed in. Revoke a session you do not recognise.';

type LoadStatus = 'loading' | 'ready' | 'error';

export interface SessionsPanelProps {
  /**
   * Called with every action-required error other than a step-up (which the
   * registered step-up prompt completes). The host navigates to `error.path`.
   */
  onActionRequired?: (error: ApiActionRequiredError) => void;
  /** Panel heading. Defaults to "Active sessions". */
  title?: string;
  /** Panel description. */
  description?: string;
}

/**
 * The caller's live sessions (`GET /v1/me/sessions`) with the current one
 * marked, and revoke (`DELETE /v1/me/sessions/{uuid}`). Revoking another
 * session just removes its row; revoking the current session ends this tab's
 * sign-in, so the panel signs the tab out through `useAuth().logout`. Writes
 * can be gated by a step-up: mount `StepUpDialog`. Needs `AuthProvider`.
 */
export function SessionsPanel({
  onActionRequired,
  title = 'Active sessions',
  description = DEFAULT_DESCRIPTION,
}: SessionsPanelProps) {
  const { logout } = useAuth();

  const [items, setItems] = useState<SelfSession[]>([]);
  const [loadStatus, setLoadStatus] = useState<LoadStatus>('loading');
  const [loadError, setLoadError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busyUuid, setBusyUuid] = useState<string | null>(null);

  // The current session is confirmed first because revoking it signs out.
  const [currentTarget, setCurrentTarget] = useState<SelfSession | null>(null);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  const onActionRequiredRef = useRef(onActionRequired);
  useEffect(() => {
    onActionRequiredRef.current = onActionRequired;
  }, [onActionRequired]);
  const forward = useCallback(
    (err: ApiActionRequiredError) => onActionRequiredRef.current?.(err),
    [],
  );

  const loadSequence = useRef(0);

  const load = useCallback(async () => {
    const sequence = ++loadSequence.current;
    try {
      const response = await api.me.sessions.list();
      // A newer load started meanwhile; its answer is the fresh one.
      if (sequence !== loadSequence.current) return;
      setItems(response.sessions);
      setLoadError(null);
      setLoadStatus('ready');
    } catch (err) {
      if (sequence !== loadSequence.current) return;
      setLoadError(describeFailure(err, 'sessions-panel', forward));
      setLoadStatus('error');
    }
  }, [forward]);

  useEffect(() => {
    void load();
  }, [load]);

  async function revoke(session: SelfSession) {
    setBusyUuid(session.uuid);
    setError(null);
    setRevokeError(null);
    setNotice(null);
    try {
      await api.me.sessions.revoke(session.uuid);
    } catch (err) {
      const message = describeFailure(err, 'sessions-panel', forward);
      // The confirm dialog (current session) owns its error; the page banner
      // owns the others.
      if (session.current) setRevokeError(message);
      else setError(message);
      setBusyUuid(null);
      return;
    }
    setBusyUuid(null);
    if (session.current) {
      // The server ended this tab's session, so every further call would be
      // refused. Sign out locally: that clears the tokens, resets the auth
      // context and navigates to the login path.
      setCurrentTarget(null);
      await logout();
      return;
    }
    // Another session: it is gone server-side; drop it from the list.
    setNotice('Session revoked.');
    void load();
  }

  return (
    <Card className="w-full">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {notice && (
          <Alert>
            <AlertDescription>{notice}</AlertDescription>
          </Alert>
        )}
        <ErrorMessage message={error} />
        <section aria-label="Sessions" className="flex flex-col gap-3">
          {loadStatus === 'loading' && (
            <p className="text-sm text-muted-foreground" role="status">
              Loading sessions...
            </p>
          )}
          {loadStatus === 'error' && (
            <div className="flex flex-col gap-2">
              <ErrorMessage message={loadError} />
              <div>
                <Button type="button" variant="outline" onClick={() => void load()}>
                  Retry
                </Button>
              </div>
            </div>
          )}
          {loadStatus === 'ready' && items.length === 0 && (
            <p className="text-sm text-muted-foreground">No active sessions.</p>
          )}
          {loadStatus === 'ready' && items.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Session</TableHead>
                  <TableHead>Signed in</TableHead>
                  <TableHead>Last seen</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead>
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((session, index) => (
                  <TableRow key={session.uuid}>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {session.current && <Badge>This session</Badge>}
                        {session.impersonating && <Badge variant="secondary">Impersonation</Badge>}
                        {session.amr.map((method) => (
                          <Badge key={method} variant="secondary">
                            {method}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell>
                      <span title={session.auth_time}>{formatDateTime(session.auth_time)}</span>
                    </TableCell>
                    <TableCell>{formatDateTime(session.last_seen_at)}</TableCell>
                    <TableCell>{formatDateTime(session.expires_at)}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={busyUuid === session.uuid}
                        aria-label={
                          session.current
                            ? 'Sign out of this session'
                            : `Revoke session ${index + 1}`
                        }
                        onClick={() => {
                          if (session.current) {
                            // Revoking the current session signs this tab out,
                            // so ask first.
                            setRevokeError(null);
                            setCurrentTarget(session);
                          } else {
                            void revoke(session);
                          }
                        }}
                      >
                        {session.current ? 'Sign out' : 'Revoke'}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </section>
      </CardContent>

      <Dialog
        open={currentTarget !== null}
        onOpenChange={(open) => {
          if (!open && busyUuid === null) setCurrentTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Sign out of this session?</DialogTitle>
            <DialogDescription>
              This is the session you are using now. Revoking it signs you out of this browser tab.
            </DialogDescription>
          </DialogHeader>
          <ErrorMessage message={revokeError} />
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setCurrentTarget(null)}
              disabled={busyUuid !== null}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={busyUuid !== null}
              onClick={() => currentTarget && void revoke(currentTarget)}
            >
              {busyUuid !== null ? 'Signing out...' : 'Sign out'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
