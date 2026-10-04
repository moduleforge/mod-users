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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from './ui/table';
import { cn } from '../lib/utils';
import { api, ApiActionRequiredError, ApiRequestError } from '../lib/api';
import type { SSHKey } from '../lib/api';

// ─── Constants and copy ──────────────────────────────────────────────────────

const LIST_LIMIT = 200;
const STEP_UP_REQUIRED_CODE = 'users.step_up_required';
const EMAIL_UNVERIFIED_CODE = 'users.email_unverified';
const STEP_UP_CODE_LENGTH = 6;
const RESEND_COOLDOWN_SECONDS = 30;

const DEFAULT_DESCRIPTION =
  'These keys authenticate git operations over SSH. Register the public half of a key pair; the private key never leaves your machine.';

const EMAIL_UNVERIFIED_NOTICE =
  'SSH keys can be added or removed only after your email address is verified.';

const REVOKE_MASKED_MESSAGE =
  'This key could not be revoked. It may already have been removed.';

/** Friendly copy for the server's known SSH-key detail codes. */
const DETAIL_COPY: Record<string, string> = {
  'users.ssh_key_in_use': 'This key is already registered.',
  'users.ssh_key_invalid':
    'Paste exactly one public key line, without options (for example, the contents of your id_ed25519.pub file).',
  'users.ssh_key_type_unsupported':
    'This key type is not supported. Use Ed25519, a FIDO (sk-) key, ECDSA, or RSA of at least 2048 bits.',
  'users.ssh_key_too_weak':
    'This key is too weak to register. Use a stronger key (RSA keys must be at least 2048 bits).',
  'users.ssh_key_label_too_long': 'The label is too long (100 characters at most).',
  'users.ssh_key_label_invalid':
    'The label contains control or bidirectional-text characters. Use plain text.',
};

type FieldName = 'public_key' | 'label';
type FieldErrors = Record<FieldName, FieldErrorData[]>;

const NO_FIELD_ERRORS: FieldErrors = { public_key: [], label: [] };

/** A gated operation held while the user completes the inline step-up. */
type PendingOperation =
  | { kind: 'register'; publicKey: string; label: string | undefined }
  | { kind: 'revoke'; key: SSHKey };

type LoadStatus = 'loading' | 'ready' | 'error';

// ─── Public API ──────────────────────────────────────────────────────────────

export interface SSHKeysPanelProps {
  /**
   * Called with every action-required error except `users.step_up_required`
   * (which the panel completes inline). The host navigates to `error.path`,
   * for example `/verify-email`. The panel never navigates itself.
   */
  onActionRequired?: (error: ApiActionRequiredError) => void;
  /** Panel heading. Defaults to "SSH keys". */
  title?: string;
  /** Panel description. Defaults to a line explaining the keys authenticate git over SSH. */
  description?: string;
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Native textarea styled to match core-gui's `Input` (core-gui has no Textarea). */
const TEXTAREA_CLASSES =
  'border-input placeholder:text-muted-foreground selection:bg-primary selection:text-primary-foreground flex min-h-24 w-full min-w-0 rounded-lg border bg-transparent px-3 py-2 font-mono text-base shadow-xs transition-[color,box-shadow] outline-none disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm ' +
  'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 ' +
  'aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40';

function formatDate(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleDateString();
}

function keyDisplayName(key: SSHKey): string {
  return key.label || key.fingerprint;
}

/** Maps `ApiRequestError.details` onto the two form fields; unknown fields are dropped. */
function mapFieldErrors(details: FieldErrorData[] | undefined): FieldErrors {
  const mapped: FieldErrors = { public_key: [], label: [] };
  for (const detail of details ?? []) {
    if (detail.field !== 'public_key' && detail.field !== 'label') continue;
    mapped[detail.field].push({
      ...detail,
      message: DETAIL_COPY[detail.code] ?? detail.message,
    });
  }
  return mapped;
}

function hasFieldErrors(errors: FieldErrors): boolean {
  return errors.public_key.length > 0 || errors.label.length > 0;
}

// ─── Component ───────────────────────────────────────────────────────────────

export function SSHKeysPanel({
  onActionRequired,
  title = 'SSH keys',
  description = DEFAULT_DESCRIPTION,
}: SSHKeysPanelProps) {
  const baseId = useId();
  const keyFieldId = `${baseId}-public-key`;
  const labelFieldId = `${baseId}-label`;
  const codeFieldId = `${baseId}-step-up-code`;

  // List state.
  const [items, setItems] = useState<SSHKey[]>([]);
  const [total, setTotal] = useState(0);
  const [loadStatus, setLoadStatus] = useState<LoadStatus>('loading');
  const [loadError, setLoadError] = useState<string | null>(null);

  // Add-form state.
  const [publicKey, setPublicKey] = useState('');
  const [label, setLabel] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>(NO_FIELD_ERRORS);
  const [formError, setFormError] = useState<string | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);

  // Revoke state.
  const [revokeTarget, setRevokeTarget] = useState<SSHKey | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  // Inline notice for delegated action-required responses.
  const [notice, setNotice] = useState<string | null>(null);

  // Inline step-up challenge. The step-up token itself is never held in state:
  // it lives only in a local variable inside `handleVerify`.
  const [pending, setPending] = useState<PendingOperation | null>(null);
  const [codeSent, setCodeSent] = useState(false);
  const [code, setCode] = useState('');
  const [cooldown, setCooldown] = useState(0);
  const [stepUpBusy, setStepUpBusy] = useState(false);
  const [stepUpError, setStepUpError] = useState<string | null>(null);

  // Keep the latest callback without making it an effect/callback dependency.
  const onActionRequiredRef = useRef(onActionRequired);
  useEffect(() => {
    onActionRequiredRef.current = onActionRequired;
  }, [onActionRequired]);

  // Ignore responses from superseded list loads.
  const loadSequence = useRef(0);

  /** Delegates a non-step-up action-required error: callback plus inline alert. */
  const handleActionRequired = useCallback((error: ApiActionRequiredError) => {
    setNotice(
      error.code === EMAIL_UNVERIFIED_CODE ? EMAIL_UNVERIFIED_NOTICE : error.message,
    );
    onActionRequiredRef.current?.(error);
  }, []);

  const loadKeys = useCallback(async () => {
    const sequence = ++loadSequence.current;
    try {
      const response = await api.sshKeys.list({ limit: LIST_LIMIT });
      if (sequence !== loadSequence.current) return;
      setItems(response.items);
      setTotal(response.total);
      setLoadError(null);
      setLoadStatus('ready');
    } catch (err) {
      if (sequence !== loadSequence.current) return;
      if (err instanceof ApiActionRequiredError) {
        handleActionRequired(err);
        setLoadError('Your SSH keys could not be loaded.');
      } else if (err instanceof ApiRequestError) {
        setLoadError(err.message);
      } else {
        console.error('[ssh-keys-panel] list', err);
        setLoadError('Something went wrong. Check the browser console for details.');
      }
      setLoadStatus('error');
    }
  }, [handleActionRequired]);

  useEffect(() => {
    void loadKeys();
  }, [loadKeys]);

  // Resend cooldown ticker.
  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setTimeout(() => setCooldown((value) => value - 1), 1000);
    return () => clearTimeout(timer);
  }, [cooldown]);

  // ─── Gated operations ───────────────────────────────────────────────────

  /** Opens a fresh step-up challenge holding `operation`. */
  function beginStepUp(operation: PendingOperation) {
    setPending(operation);
    setCodeSent(false);
    setCode('');
    setCooldown(0);
    setStepUpError(null);
  }

  function resetStepUp() {
    setPending(null);
    setCodeSent(false);
    setCode('');
    setCooldown(0);
    setStepUpError(null);
  }

  /**
   * Runs one attempt of `operation`, optionally with a step-up token (used
   * once, by the caller's local variable only). Handles every outcome,
   * including re-opening the challenge when step-up is demanded again.
   */
  async function execute(operation: PendingOperation, stepUpToken?: string) {
    const options = stepUpToken ? { stepUpToken } : undefined;
    try {
      if (operation.kind === 'register') {
        await api.sshKeys.register(
          {
            public_key: operation.publicKey,
            ...(operation.label !== undefined ? { label: operation.label } : {}),
          },
          options,
        );
        setPublicKey('');
        setLabel('');
        setSuccessMessage('SSH key added.');
      } else {
        await api.sshKeys.revoke(operation.key.uuid, options);
        setRevokeTarget(null);
      }
      void loadKeys();
    } catch (err) {
      handleOperationError(operation, err);
    }
  }

  function handleOperationError(operation: PendingOperation, err: unknown) {
    if (err instanceof ApiActionRequiredError) {
      if (operation.kind === 'revoke') setRevokeTarget(null);
      if (err.code === STEP_UP_REQUIRED_CODE) {
        beginStepUp(operation);
      } else {
        handleActionRequired(err);
      }
      return;
    }

    if (operation.kind === 'register') {
      if (err instanceof ApiRequestError) {
        const mapped = mapFieldErrors(err.details);
        setFieldErrors(mapped);
        if (!hasFieldErrors(mapped)) setFormError(err.message);
      } else {
        console.error('[ssh-keys-panel] register', err);
        setFormError('Something went wrong. Check the browser console for details.');
      }
      return;
    }

    // Revoke failure: close the dialog and report in the panel.
    setRevokeTarget(null);
    if (err instanceof ApiRequestError) {
      if (err.status === 403) {
        setRevokeError(REVOKE_MASKED_MESSAGE);
        void loadKeys();
      } else {
        setRevokeError(err.message);
      }
    } else {
      console.error('[ssh-keys-panel] revoke', err);
      setRevokeError('Something went wrong. Check the browser console for details.');
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const trimmedKey = publicKey.trim();
    if (!trimmedKey || submitting) return;
    const trimmedLabel = label.trim();

    setFieldErrors(NO_FIELD_ERRORS);
    setFormError(null);
    setSuccessMessage(null);
    setNotice(null);
    setSubmitting(true);
    try {
      await execute({
        kind: 'register',
        publicKey: trimmedKey,
        label: trimmedLabel === '' ? undefined : trimmedLabel,
      });
    } finally {
      setSubmitting(false);
    }
  }

  function openRevokeDialog(key: SSHKey) {
    setRevokeError(null);
    setSuccessMessage(null);
    setNotice(null);
    setRevokeTarget(key);
  }

  async function handleConfirmRevoke() {
    if (!revokeTarget || revoking) return;
    setRevoking(true);
    try {
      await execute({ kind: 'revoke', key: revokeTarget });
    } finally {
      setRevoking(false);
    }
  }

  // ─── Step-up challenge ──────────────────────────────────────────────────

  async function handleSendCode() {
    setStepUpBusy(true);
    setStepUpError(null);
    try {
      await api.stepUp.request();
      setCodeSent(true);
      setCooldown(RESEND_COOLDOWN_SECONDS);
    } catch (err) {
      if (err instanceof ApiActionRequiredError) {
        resetStepUp();
        handleActionRequired(err);
      } else if (err instanceof ApiRequestError) {
        setStepUpError(err.message);
      } else {
        console.error('[ssh-keys-panel] step-up request', err);
        setStepUpError('Something went wrong. Check the browser console for details.');
      }
    } finally {
      setStepUpBusy(false);
    }
  }

  async function handleVerify(e: React.FormEvent) {
    e.preventDefault();
    const operation = pending;
    const trimmedCode = code.trim();
    if (!operation || trimmedCode.length !== STEP_UP_CODE_LENGTH || stepUpBusy) return;

    setStepUpBusy(true);
    setStepUpError(null);

    // The token lives only in this local variable: used for exactly one
    // retry below, then dropped. Never stored, logged, or rendered.
    let token: string;
    try {
      const verified = await api.stepUp.verify(trimmedCode);
      token = verified.step_up_token;
    } catch (err) {
      if (err instanceof ApiActionRequiredError) {
        resetStepUp();
        handleActionRequired(err);
      } else if (err instanceof ApiRequestError && err.status === 401) {
        setStepUpError('That code is wrong or has expired.');
      } else if (err instanceof ApiRequestError) {
        setStepUpError(err.message);
      } else {
        console.error('[ssh-keys-panel] step-up verify', err);
        setStepUpError('Something went wrong. Check the browser console for details.');
      }
      setStepUpBusy(false);
      return;
    }

    // Verified: close the challenge and retry the held operation exactly once.
    resetStepUp();
    if (operation.kind === 'register') setSubmitting(true);
    try {
      await execute(operation, token);
    } finally {
      if (operation.kind === 'register') setSubmitting(false);
      setStepUpBusy(false);
    }
  }

  // ─── Render ─────────────────────────────────────────────────────────────

  const keyBlank = publicKey.trim() === '';
  const challengeOpen = pending !== null;
  const showTruncationNote = loadStatus === 'ready' && total > items.length;

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

        {/* ── List ── */}
        <section aria-label="Registered SSH keys" className="flex flex-col gap-3">
          <ErrorMessage message={revokeError} />
          {loadStatus === 'loading' && (
            <p className="text-sm text-muted-foreground" role="status">
              Loading SSH keys...
            </p>
          )}
          {loadStatus === 'error' && (
            <div className="flex flex-col gap-2">
              <ErrorMessage message={loadError} />
              <div>
                <Button type="button" variant="outline" onClick={() => void loadKeys()}>
                  Retry
                </Button>
              </div>
            </div>
          )}
          {loadStatus === 'ready' && items.length === 0 && (
            <p className="text-sm text-muted-foreground">No SSH keys yet.</p>
          )}
          {loadStatus === 'ready' && items.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Label</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Fingerprint</TableHead>
                  <TableHead>Added</TableHead>
                  <TableHead>
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((key) => (
                  <TableRow key={key.uuid}>
                    <TableCell>
                      {key.label ? <bdi>{key.label}</bdi> : 'Untitled key'}
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">{key.key_type}</Badge>
                    </TableCell>
                    <TableCell className="max-w-64">
                      <span
                        className="block break-all font-mono text-xs"
                        title={key.fingerprint}
                      >
                        {key.fingerprint}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span title={key.created_at}>{formatDate(key.created_at)}</span>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        aria-label={`Revoke ${keyDisplayName(key)}`}
                        onClick={() => openRevokeDialog(key)}
                      >
                        Revoke
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {showTruncationNote && (
            <p className="text-sm text-muted-foreground">
              Showing {items.length} of {total} keys
            </p>
          )}
        </section>

        {/* ── Step-up challenge ── */}
        {pending && (
          <section
            aria-label="Confirm it's you"
            className="flex flex-col gap-3 rounded-lg border p-4"
          >
            <div className="flex flex-col gap-1">
              <h3 className="text-sm font-medium">Confirm it&apos;s you</h3>
              <p className="text-sm text-muted-foreground">
                {pending.kind === 'register'
                  ? 'To add this key, we need to confirm it’s you.'
                  : 'To revoke this key, we need to confirm it’s you.'}{' '}
                We&apos;ll email you a verification code.
              </p>
              {pending.kind === 'revoke' && (
                <p className="text-sm">
                  Key: <bdi>{keyDisplayName(pending.key)}</bdi>
                </p>
              )}
            </div>
            {stepUpError && <ErrorMessage message={stepUpError} />}
            {!codeSent ? (
              <div className="flex gap-2">
                <Button type="button" onClick={() => void handleSendCode()} disabled={stepUpBusy}>
                  Send code
                </Button>
                <Button type="button" variant="outline" onClick={resetStepUp} disabled={stepUpBusy}>
                  Cancel
                </Button>
              </div>
            ) : (
              <form onSubmit={(e) => void handleVerify(e)} className="flex flex-col gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={codeFieldId}>Verification code</Label>
                  <Input
                    id={codeFieldId}
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    maxLength={STEP_UP_CODE_LENGTH}
                    value={code}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => setCode(e.target.value)}
                    placeholder="123456"
                    className="max-w-40 font-mono"
                  />
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="submit"
                    disabled={stepUpBusy || code.trim().length !== STEP_UP_CODE_LENGTH}
                  >
                    Verify and continue
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => void handleSendCode()}
                    disabled={stepUpBusy || cooldown > 0}
                  >
                    {cooldown > 0 ? `Resend code (${cooldown}s)` : 'Resend code'}
                  </Button>
                  <Button type="button" variant="ghost" onClick={resetStepUp} disabled={stepUpBusy}>
                    Cancel
                  </Button>
                </div>
              </form>
            )}
          </section>
        )}

        {/* ── Add key ── */}
        <form onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-4">
          <h3 className="text-sm font-medium">Add an SSH key</h3>
          <ErrorMessage message={formError} />
          {successMessage && (
            <p className="text-sm text-green-700 dark:text-green-400" role="status">
              {successMessage}
            </p>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={keyFieldId}>Public key</Label>
            <textarea
              id={keyFieldId}
              data-slot="textarea"
              className={cn(TEXTAREA_CLASSES)}
              required
              autoComplete="off"
              spellCheck={false}
              rows={3}
              placeholder="ssh-ed25519 AAAA... you@example.com"
              value={publicKey}
              onChange={(e) => setPublicKey(e.target.value)}
              aria-invalid={fieldErrors.public_key.length > 0 || undefined}
              aria-describedby={
                fieldErrors.public_key.length > 0 ? `${keyFieldId}-error` : undefined
              }
              disabled={submitting}
            />
            {fieldErrors.public_key.map((error, index) => (
              <FieldError key={`${error.code}-${index}`} error={error} id={`${keyFieldId}-error`} />
            ))}
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={labelFieldId}>Label (optional)</Label>
            <Input
              id={labelFieldId}
              maxLength={100}
              autoComplete="off"
              placeholder="Work laptop"
              value={label}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setLabel(e.target.value)}
              aria-invalid={fieldErrors.label.length > 0 || undefined}
              aria-describedby={fieldErrors.label.length > 0 ? `${labelFieldId}-error` : undefined}
              disabled={submitting}
            />
            {fieldErrors.label.map((error, index) => (
              <FieldError key={`${error.code}-${index}`} error={error} id={`${labelFieldId}-error`} />
            ))}
          </div>
          <div>
            <Button type="submit" disabled={submitting || keyBlank || challengeOpen}>
              {submitting ? 'Adding...' : 'Add key'}
            </Button>
          </div>
        </form>
      </CardContent>

      {/* ── Revoke confirmation ── */}
      <Dialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open && !revoking) setRevokeTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Revoke SSH key?</DialogTitle>
            <DialogDescription>
              This key stops working for new SSH connections.
            </DialogDescription>
          </DialogHeader>
          {revokeTarget && (
            <dl className="flex flex-col gap-1 text-sm">
              <div>
                <dt className="text-muted-foreground">Label</dt>
                <dd>
                  <bdi>{revokeTarget.label || 'Untitled key'}</bdi>
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Fingerprint</dt>
                <dd className="break-all font-mono text-xs">{revokeTarget.fingerprint}</dd>
              </div>
            </dl>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setRevokeTarget(null)}
              disabled={revoking}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={() => void handleConfirmRevoke()}
              disabled={revoking}
            >
              {revoking ? 'Revoking...' : 'Revoke key'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
