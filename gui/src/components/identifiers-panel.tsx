'use client';

import { useCallback, useEffect, useId, useRef, useState } from 'react';
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
  FieldError,
  Input,
  Label,
} from '@moduleforge/core-gui';
import type { FieldErrorData } from '@moduleforge/core-gui';
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
import {
  api,
  ApiRequestError,
  type ApiActionRequiredError,
  type IdentifierView,
} from '../lib/api';
import {
  EMAIL_CODE_LENGTH,
  RESEND_COOLDOWN_SECONDS,
  describeFailure,
  fieldErrorsOf,
  formatDate,
} from '../lib/self-service';

const DEFAULT_DESCRIPTION =
  'The email addresses on your account. Verify an address to use it, and choose which one is primary.';

type LoadStatus = 'loading' | 'ready' | 'error';

export interface IdentifiersPanelProps {
  /**
   * Called with every action-required error other than a step-up (which the
   * registered step-up prompt completes). The host navigates to `error.path`.
   */
  onActionRequired?: (error: ApiActionRequiredError) => void;
  /** Panel heading. Defaults to "Email addresses". */
  title?: string;
  /** Panel description. */
  description?: string;
}

/**
 * The caller's identifiers (`/v1/me/identifiers`): list, add an email address,
 * email a verification code, verify, set the primary address and delete.
 * Writes are gated by a step-up: mount `StepUpDialog` (or register a step-up
 * handler). Needs `AuthProvider`; the auth context is refreshed after a change
 * so `emailVerified` stays correct.
 */
export function IdentifiersPanel({
  onActionRequired,
  title = 'Email addresses',
  description = DEFAULT_DESCRIPTION,
}: IdentifiersPanelProps) {
  const { refreshUser } = useAuth();
  const baseId = useId();
  const addId = `${baseId}-add`;
  const codeId = `${baseId}-code`;

  const [items, setItems] = useState<IdentifierView[]>([]);
  const [loadStatus, setLoadStatus] = useState<LoadStatus>('loading');
  const [loadError, setLoadError] = useState<string | null>(null);

  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  // The uuid of the row an action is running against.
  const [rowBusy, setRowBusy] = useState<string | null>(null);

  // Add form.
  const [email, setEmail] = useState('');
  const [adding, setAdding] = useState(false);
  const [addErrors, setAddErrors] = useState<FieldErrorData[]>([]);
  const [addError, setAddError] = useState<string | null>(null);

  // Verify dialog.
  const [verifyTarget, setVerifyTarget] = useState<IdentifierView | null>(null);
  const [code, setCode] = useState('');
  const [verifying, setVerifying] = useState(false);
  const [verifyError, setVerifyError] = useState<string | null>(null);
  const [cooldown, setCooldown] = useState(0);

  // Delete dialog.
  const [deleteTarget, setDeleteTarget] = useState<IdentifierView | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

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
      const response = await api.me.identifiers.list();
      // A newer load started meanwhile; its answer is the fresh one.
      if (sequence !== loadSequence.current) return;
      setItems(response.identifiers);
      setLoadError(null);
      setLoadStatus('ready');
    } catch (err) {
      if (sequence !== loadSequence.current) return;
      setLoadError(describeFailure(err, 'identifiers-panel', forward));
      setLoadStatus('error');
    }
  }, [forward]);

  useEffect(() => {
    void load();
  }, [load]);

  // Resend cooldown ticker.
  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setTimeout(() => setCooldown((value) => value - 1), 1000);
    return () => clearTimeout(timer);
  }, [cooldown]);

  /** Re-reads the list and the auth context after any change. */
  function changed() {
    void load();
    // `emailVerified` and the primary email on the user record may have moved.
    void refreshUser();
  }

  async function submitAdd(e: React.FormEvent) {
    e.preventDefault();
    const value = email.trim();
    if (value === '' || adding) return;
    setAdding(true);
    setAddErrors([]);
    setAddError(null);
    setNotice(null);
    try {
      await api.me.identifiers.add({ kind: 'email', value });
      setEmail('');
      setNotice('Address added. Send a verification code to verify it.');
      changed();
    } catch (err) {
      const mapped = fieldErrorsOf(err, ['value']).value ?? [];
      setAddErrors(mapped);
      // A duplicate arrives as a field detail on `value`; anything else (a
      // declined step-up, a server fault) has no field and needs the banner.
      if (mapped.length === 0) setAddError(describeFailure(err, 'identifiers-panel', forward));
    } finally {
      setAdding(false);
    }
  }

  async function sendCode(identifier: IdentifierView, inDialog: boolean): Promise<boolean> {
    setRowBusy(identifier.uuid);
    setError(null);
    try {
      await api.me.identifiers.resendVerification(identifier.uuid);
      setCooldown(RESEND_COOLDOWN_SECONDS);
      return true;
    } catch (err) {
      const message = describeFailure(err, 'identifiers-panel', forward);
      // Route by the caller's explicit target, not the render-time verifyTarget
      // state: openVerify sets verifyTarget and calls this in the same tick, so
      // this closure still sees null. inDialog=true implies the dialog is (or is
      // about to be) open and must show the failure; false implies a page-level
      // row action whose failure belongs in the page banner.
      if (inDialog) setVerifyError(message);
      else setError(message);
      return false;
    } finally {
      setRowBusy(null);
    }
  }

  async function openVerify(identifier: IdentifierView) {
    setNotice(null);
    setVerifyError(null);
    setCode('');
    setVerifyTarget(identifier);
    // Opening the dialog also emails the code, so the user is never asked for
    // a code that was not sent; a failure shows inside the open dialog.
    await sendCode(identifier, true);
  }

  async function submitVerify(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = code.trim();
    if (!verifyTarget || trimmed.length !== EMAIL_CODE_LENGTH || verifying) return;
    setVerifying(true);
    setVerifyError(null);
    try {
      await api.me.identifiers.verify(verifyTarget.uuid, trimmed);
      setVerifyTarget(null);
      setNotice('Address verified.');
      changed();
    } catch (err) {
      // The API answers a wrong or expired code with 400, so that status
      // means "try the code again"; any other failure keeps the server text.
      if (err instanceof ApiRequestError && err.status === 400) {
        setVerifyError('That code is wrong or has expired.');
      } else {
        setVerifyError(describeFailure(err, 'identifiers-panel', forward));
      }
    } finally {
      setVerifying(false);
    }
  }

  async function makePrimary(identifier: IdentifierView) {
    setRowBusy(identifier.uuid);
    setError(null);
    setNotice(null);
    try {
      await api.me.identifiers.setPrimary(identifier.uuid);
      setNotice(`${identifier.value} is now your primary address.`);
      changed();
    } catch (err) {
      setError(describeFailure(err, 'identifiers-panel', forward));
    } finally {
      setRowBusy(null);
    }
  }

  async function confirmDelete() {
    if (!deleteTarget || deleting) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await api.me.identifiers.delete(deleteTarget.uuid);
      setDeleteTarget(null);
      setNotice('Address removed.');
      changed();
    } catch (err) {
      // Shown inside the dialog so the refusal (for example an address still
      // in use) sits next to the action that caused it.
      setDeleteError(describeFailure(err, 'identifiers-panel', forward));
    } finally {
      setDeleting(false);
    }
  }

  return (
    <Card className="w-full">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {notice && (
          <Alert>
            <AlertDescription>{notice}</AlertDescription>
          </Alert>
        )}
        <ErrorMessage message={error} />

        <section aria-label="Email addresses" className="flex flex-col gap-3">
          {loadStatus === 'loading' && (
            <p className="text-sm text-muted-foreground" role="status">
              Loading addresses...
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
            <p className="text-sm text-muted-foreground">No email addresses yet.</p>
          )}
          {loadStatus === 'ready' && items.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Address</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Added</TableHead>
                  <TableHead>
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((identifier) => (
                  <TableRow key={identifier.uuid}>
                    <TableCell>
                      <bdi>{identifier.value}</bdi>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {identifier.is_primary && <Badge>Primary</Badge>}
                        <Badge variant="secondary">
                          {identifier.verified ? 'Verified' : 'Unverified'}
                        </Badge>
                      </div>
                    </TableCell>
                    <TableCell>{formatDate(identifier.created_at)}</TableCell>
                    <TableCell className="text-right">
                      <div className="flex flex-wrap justify-end gap-2">
                        {/* Only an unverified address can be verified. */}
                        {!identifier.verified && (
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={rowBusy === identifier.uuid}
                            aria-label={`Verify ${identifier.value}`}
                            onClick={() => void openVerify(identifier)}
                          >
                            Verify
                          </Button>
                        )}
                        {/* The primary address must be a verified one, and
                            an address that already is primary needs no action. */}
                        {identifier.verified && !identifier.is_primary && (
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={rowBusy === identifier.uuid}
                            aria-label={`Make ${identifier.value} primary`}
                            onClick={() => void makePrimary(identifier)}
                          >
                            Make primary
                          </Button>
                        )}
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          aria-label={`Delete ${identifier.value}`}
                          onClick={() => {
                            setDeleteError(null);
                            setNotice(null);
                            setDeleteTarget(identifier);
                          }}
                        >
                          Delete
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </section>

        <form onSubmit={(e) => void submitAdd(e)} className="flex flex-col gap-4">
          <h3 className="text-sm font-medium">Add an email address</h3>
          <ErrorMessage message={addError} />
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={addId}>Email</Label>
            <Input
              id={addId}
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setEmail(e.target.value)}
              aria-invalid={addErrors.length > 0 || undefined}
              aria-describedby={addErrors.length > 0 ? `${addId}-error` : undefined}
              disabled={adding}
            />
            {addErrors.map((fieldError, index) => (
              <FieldError key={`${fieldError.code}-${index}`} error={fieldError} id={`${addId}-error`} />
            ))}
          </div>
          <div>
            <Button type="submit" disabled={adding || email.trim() === ''}>
              {adding ? 'Adding...' : 'Add address'}
            </Button>
          </div>
        </form>
      </CardContent>

      {/* ── Verify ── */}
      <Dialog
        open={verifyTarget !== null}
        onOpenChange={(open) => {
          if (!open && !verifying) setVerifyTarget(null);
        }}
      >
        <DialogContent>
          <form onSubmit={(e) => void submitVerify(e)} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Verify address</DialogTitle>
              <DialogDescription>
                {verifyTarget
                  ? `Enter the code we emailed to ${verifyTarget.value}.`
                  : 'Enter the emailed code.'}
              </DialogDescription>
            </DialogHeader>
            <ErrorMessage message={verifyError} />
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={codeId}>Verification code</Label>
              <Input
                id={codeId}
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={EMAIL_CODE_LENGTH}
                placeholder="123456"
                className="max-w-40 font-mono"
                value={code}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setCode(e.target.value)}
              />
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={verifying || cooldown > 0 || rowBusy !== null}
                onClick={() => verifyTarget && void sendCode(verifyTarget, true)}
              >
                {cooldown > 0 ? `Resend code (${cooldown}s)` : 'Resend code'}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setVerifyTarget(null)} disabled={verifying}>
                Cancel
              </Button>
              <Button type="submit" disabled={verifying || code.trim().length !== EMAIL_CODE_LENGTH}>
                {verifying ? 'Verifying...' : 'Verify'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* ── Delete confirmation ── */}
      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open && !deleting) setDeleteTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete address?</DialogTitle>
            <DialogDescription>
              The address is removed from your account.
            </DialogDescription>
          </DialogHeader>
          {deleteTarget && (
            <p className="text-sm">
              <bdi>{deleteTarget.value}</bdi>
            </p>
          )}
          <ErrorMessage message={deleteError} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setDeleteTarget(null)} disabled={deleting}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" onClick={() => void confirmDelete()} disabled={deleting}>
              {deleting ? 'Deleting...' : 'Delete address'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
