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
import { GuestUpgradeForm } from './guest-upgrade-form';
import { profileLabel } from './profile-switcher';
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
import { cn } from '../lib/utils';
import {
  api,
  type ApiActionRequiredError,
  type CredentialView,
  type OIDCProvider,
} from '../lib/api';
import { isSafeReturnPath } from '../lib/return-path';
import {
  SELF_SERVICE_CODES,
  describeFailure,
  fieldErrorsOf,
  formatDate,
  hasErrorCode,
  isWebUrl,
} from '../lib/self-service';

const DEFAULT_DESCRIPTION =
  'The ways you can sign in: a password, an emailed code, a linked provider or an SSH key. Keep at least one.';

/** Readable names for the credential kinds the API returns; others show raw. */
const KIND_LABELS: Record<string, string> = {
  password: 'Password',
  email_otp: 'Emailed code',
  oidc: 'Linked provider',
  ssh_key: 'SSH key',
  api_key: 'API key',
  anon: 'Guest device',
};

/** Native textarea styled to match core-gui's `Input` (core-gui has no Textarea). */
const TEXTAREA_CLASSES =
  'border-input placeholder:text-muted-foreground selection:bg-primary selection:text-primary-foreground flex min-h-24 w-full min-w-0 rounded-lg border bg-transparent px-3 py-2 font-mono text-base shadow-xs transition-[color,box-shadow] outline-none disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50 md:text-sm ' +
  'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 ' +
  'aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40';

/** Native select styled to match `Input`. */
const SELECT_CLASSES =
  'border-input flex h-9 w-full min-w-0 rounded-lg border bg-transparent px-3 py-1 text-base shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 md:text-sm';

const PASSWORD_FIELDS = ['password', 'label'] as const;
const SSH_FIELDS = ['public_key', 'label'] as const;

type LoadStatus = 'loading' | 'ready' | 'error';

export interface CredentialsPanelProps {
  /**
   * Called with every action-required error other than a step-up (which the
   * registered step-up prompt completes). The host navigates to `error.path`,
   * for example `/verify-email`. The panel never navigates itself.
   */
  onActionRequired?: (error: ApiActionRequiredError) => void;
  /** Panel heading. Defaults to "Sign-in methods". */
  title?: string;
  /** Panel description. */
  description?: string;
  /**
   * Where the provider sends the browser back to after linking. Defaults to
   * the current path when it is a safe site path.
   */
  oidcReturnPath?: string;
}

/** Name used for a credential in buttons and confirmations. */
function credentialName(credential: CredentialView): string {
  return (
    credential.label ||
    credential.fingerprint ||
    credential.email_at_link ||
    KIND_LABELS[credential.kind] ||
    credential.kind
  );
}

/**
 * The caller's credentials (`/v1/me/credentials`): list by kind, add a
 * password or SSH key, link an OIDC provider, edit the label and landing
 * profile, and revoke. For a guest the add controls give way to
 * `GuestUpgradeForm`, because the API refuses every credential add with
 * `409 users.guest_upgrade_required` until the guest upgrades. Writes are
 * gated by a step-up: mount `StepUpDialog` (or register a step-up handler) so
 * the prompt can run. Needs `AuthProvider`.
 */
export function CredentialsPanel({
  onActionRequired,
  title = 'Sign-in methods',
  description = DEFAULT_DESCRIPTION,
  oidcReturnPath,
}: CredentialsPanelProps) {
  const { user, profiles, refreshUser } = useAuth();
  const baseId = useId();

  const [items, setItems] = useState<CredentialView[]>([]);
  const [loadStatus, setLoadStatus] = useState<LoadStatus>('loading');
  const [loadError, setLoadError] = useState<string | null>(null);
  const [providers, setProviders] = useState<OIDCProvider[]>([]);

  // Notices are the panel-level result line; errors are the red banner.
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  // A 409 users.guest_upgrade_required reached us although the user record said
  // otherwise (stale), so the upgrade form is shown from here on.
  const [guestRequired, setGuestRequired] = useState(false);

  // Add-password form.
  const [newPassword, setNewPassword] = useState('');
  const [passwordLabel, setPasswordLabel] = useState('');
  const [passwordBusy, setPasswordBusy] = useState(false);
  const [passwordErrors, setPasswordErrors] = useState<Record<string, FieldErrorData[]>>({});
  const [passwordFormError, setPasswordFormError] = useState<string | null>(null);

  // Add-SSH-key form.
  const [publicKey, setPublicKey] = useState('');
  const [keyLabel, setKeyLabel] = useState('');
  const [keyBusy, setKeyBusy] = useState(false);
  const [keyErrors, setKeyErrors] = useState<Record<string, FieldErrorData[]>>({});
  const [keyFormError, setKeyFormError] = useState<string | null>(null);

  // Edit dialog.
  const [editTarget, setEditTarget] = useState<CredentialView | null>(null);
  const [editLabel, setEditLabel] = useState('');
  const [editProfile, setEditProfile] = useState('');
  const [editBusy, setEditBusy] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);

  // Revoke dialog.
  const [revokeTarget, setRevokeTarget] = useState<CredentialView | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  const [linkBusy, setLinkBusy] = useState(false);

  // Keep the latest callback without making it an effect/callback dependency.
  const onActionRequiredRef = useRef(onActionRequired);
  useEffect(() => {
    onActionRequiredRef.current = onActionRequired;
  }, [onActionRequired]);
  const forward = useCallback(
    (err: ApiActionRequiredError) => onActionRequiredRef.current?.(err),
    [],
  );

  // Ignore responses from superseded list loads.
  const loadSequence = useRef(0);

  const load = useCallback(async () => {
    const sequence = ++loadSequence.current;
    try {
      const response = await api.me.credentials.list();
      // A newer load started meanwhile; its answer is the fresh one.
      if (sequence !== loadSequence.current) return;
      setItems(response.credentials);
      setLoadError(null);
      setLoadStatus('ready');
    } catch (err) {
      if (sequence !== loadSequence.current) return;
      setLoadError(describeFailure(err, 'credentials-panel', forward));
      setLoadStatus('error');
    }
  }, [forward]);

  useEffect(() => {
    void load();
  }, [load]);

  // The OIDC providers that can be linked; never throws.
  useEffect(() => {
    let live = true;
    void api.auth.providers().then((list) => {
      // Ignore a late answer after unmount.
      if (live) setProviders(list);
    });
    return () => {
      live = false;
    };
  }, []);

  const showUpgrade = user?.is_guest === true || guestRequired;

  /** Reports a failed write through `setMessage`, flagging a guest first. */
  function failWrite(err: unknown, setMessage: (message: string) => void) {
    if (hasErrorCode(err, SELF_SERVICE_CODES.guestUpgradeRequired)) {
      // The server says this principal is a guest: switch to the upgrade form
      // so the user can act on the message instead of retrying in vain.
      setGuestRequired(true);
    }
    setMessage(describeFailure(err, 'credentials-panel', forward));
  }

  // ─── Add a password ──────────────────────────────────────────────────────

  async function submitPassword(e: React.FormEvent) {
    e.preventDefault();
    if (newPassword === '' || passwordBusy) return;
    setPasswordBusy(true);
    setPasswordErrors({});
    setPasswordFormError(null);
    setNotice(null);
    try {
      const trimmed = passwordLabel.trim();
      await api.me.credentials.addPassword({
        password: newPassword,
        ...(trimmed !== '' ? { label: trimmed } : {}),
      });
      setNewPassword('');
      setPasswordLabel('');
      setNotice('Password added.');
      void load();
    } catch (err) {
      const mapped = fieldErrorsOf(err, PASSWORD_FIELDS);
      setPasswordErrors(mapped);
      // Field-level details render beside their inputs; anything else (a
      // conflict, a declined step-up) needs the form-level message.
      if (Object.keys(mapped).length === 0) failWrite(err, setPasswordFormError);
    } finally {
      setPasswordBusy(false);
    }
  }

  // ─── Add an SSH key ──────────────────────────────────────────────────────

  async function submitKey(e: React.FormEvent) {
    e.preventDefault();
    const trimmedKey = publicKey.trim();
    if (trimmedKey === '' || keyBusy) return;
    setKeyBusy(true);
    setKeyErrors({});
    setKeyFormError(null);
    setNotice(null);
    try {
      const trimmed = keyLabel.trim();
      await api.me.credentials.addSshKey({
        public_key: trimmedKey,
        ...(trimmed !== '' ? { label: trimmed } : {}),
      });
      setPublicKey('');
      setKeyLabel('');
      setNotice('SSH key added.');
      void load();
    } catch (err) {
      const mapped = fieldErrorsOf(err, SSH_FIELDS);
      setKeyErrors(mapped);
      // As above: unmapped failures still need a visible message.
      if (Object.keys(mapped).length === 0) failWrite(err, setKeyFormError);
    } finally {
      setKeyBusy(false);
    }
  }

  // ─── Link an OIDC provider ───────────────────────────────────────────────

  async function linkProvider(provider: OIDCProvider) {
    setLinkBusy(true);
    setError(null);
    setNotice(null);
    try {
      const here = oidcReturnPath ?? window.location.pathname;
      const { authorize_url } = await api.me.credentials.startOidcLink(
        provider.id,
        isSafeReturnPath(here) ? here : undefined,
      );
      if (!isWebUrl(authorize_url)) {
        // Only web schemes may reach the full-page navigation below.
        setError('The sign-in provider returned an address that cannot be opened.');
        return;
      }
      // Full page navigation: the provider callback links the identity and
      // returns here, so the list reloads from scratch on arrival.
      window.location.assign(authorize_url);
    } catch (err) {
      failWrite(err, setError);
    } finally {
      setLinkBusy(false);
    }
  }

  // ─── Edit ────────────────────────────────────────────────────────────────

  function openEdit(credential: CredentialView) {
    setEditTarget(credential);
    setEditLabel(credential.label ?? '');
    setEditProfile(credential.default_profile_uuid ?? '');
    setEditError(null);
    setNotice(null);
  }

  async function submitEdit(e: React.FormEvent) {
    e.preventDefault();
    if (!editTarget || editBusy) return;
    // PATCH needs at least one member and leaves absent ones unchanged, so
    // only the edited members are sent; an empty profile choice sends null to
    // clear the landing profile.
    const body: { label?: string; default_profile_uuid?: string | null } = {};
    const nextLabel = editLabel.trim();
    if (nextLabel !== (editTarget.label ?? '')) body.label = nextLabel;
    const nextProfile = editProfile === '' ? null : editProfile;
    if (nextProfile !== editTarget.default_profile_uuid) body.default_profile_uuid = nextProfile;
    if (Object.keys(body).length === 0) {
      // Nothing changed: closing is the whole outcome, and no call is made.
      setEditTarget(null);
      return;
    }
    setEditBusy(true);
    setEditError(null);
    try {
      await api.me.credentials.patch(editTarget.uuid, body);
      setEditTarget(null);
      setNotice('Credential updated.');
      void load();
    } catch (err) {
      setEditError(describeFailure(err, 'credentials-panel', forward));
    } finally {
      setEditBusy(false);
    }
  }

  // ─── Revoke ──────────────────────────────────────────────────────────────

  async function confirmRevoke() {
    if (!revokeTarget || revoking) return;
    setRevoking(true);
    setRevokeError(null);
    try {
      await api.me.credentials.delete(revokeTarget.uuid);
      setRevokeTarget(null);
      setNotice('Credential revoked. Sessions that signed in with it have ended.');
      void load();
      // Revoking can end the session this tab is using; refreshing the session
      // lets the request layer sign the tab out if it did.
      void refreshUser();
    } catch (err) {
      // users.last_credential is a 409 refusal the dialog explains in place,
      // so the user can add another method and retry.
      setRevokeError(describeFailure(err, 'credentials-panel', forward));
    } finally {
      setRevoking(false);
    }
  }

  const passwordId = `${baseId}-new-password`;
  const passwordLabelId = `${baseId}-password-label`;
  const keyId = `${baseId}-public-key`;
  const keyLabelId = `${baseId}-key-label`;
  const editLabelId = `${baseId}-edit-label`;
  const editProfileId = `${baseId}-edit-profile`;

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

        {/* ── List ── */}
        <section aria-label="Credentials" className="flex flex-col gap-3">
          {loadStatus === 'loading' && (
            <p className="text-sm text-muted-foreground" role="status">
              Loading credentials...
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
            <p className="text-sm text-muted-foreground">No credentials yet.</p>
          )}
          {loadStatus === 'ready' && items.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Kind</TableHead>
                  <TableHead>Label</TableHead>
                  <TableHead>Details</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead>
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((credential) => (
                  <TableRow key={credential.uuid}>
                    <TableCell>
                      <Badge variant="secondary">
                        {KIND_LABELS[credential.kind] ?? credential.kind}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      {credential.label ? <bdi>{credential.label}</bdi> : 'Untitled'}
                    </TableCell>
                    <TableCell className="max-w-64">
                      <CredentialDetails credential={credential} />
                    </TableCell>
                    <TableCell>
                      <span title={credential.last_used_at ?? undefined}>
                        {formatDate(credential.last_used_at)}
                      </span>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-2">
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          aria-label={`Edit ${credentialName(credential)}`}
                          onClick={() => openEdit(credential)}
                        >
                          Edit
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          aria-label={`Revoke ${credentialName(credential)}`}
                          onClick={() => {
                            setRevokeError(null);
                            setNotice(null);
                            setRevokeTarget(credential);
                          }}
                        >
                          Revoke
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </section>

        {showUpgrade ? (
          // A guest cannot add credentials; upgrading is the only way forward.
          <GuestUpgradeForm
            onActionRequired={onActionRequired}
            onUpgraded={() => {
              setGuestRequired(false);
              void load();
            }}
          />
        ) : (
          <>
            {/* ── Add a password ── */}
            <form onSubmit={(e) => void submitPassword(e)} className="flex flex-col gap-4">
              <h3 className="text-sm font-medium">Add a password</h3>
              <ErrorMessage message={passwordFormError} />
              <div className="flex flex-col gap-1.5">
                <Label htmlFor={passwordId}>New password</Label>
                <Input
                  id={passwordId}
                  type="password"
                  autoComplete="new-password"
                  value={newPassword}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setNewPassword(e.target.value)}
                  aria-invalid={(passwordErrors.password?.length ?? 0) > 0 || undefined}
                  aria-describedby={passwordErrors.password ? `${passwordId}-error` : undefined}
                  disabled={passwordBusy}
                />
                {(passwordErrors.password ?? []).map((fieldError, index) => (
                  <FieldError key={`${fieldError.code}-${index}`} error={fieldError} id={`${passwordId}-error`} />
                ))}
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor={passwordLabelId}>Label (optional)</Label>
                <Input
                  id={passwordLabelId}
                  maxLength={100}
                  autoComplete="off"
                  value={passwordLabel}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setPasswordLabel(e.target.value)}
                  disabled={passwordBusy}
                />
                {(passwordErrors.label ?? []).map((fieldError, index) => (
                  <FieldError key={`${fieldError.code}-${index}`} error={fieldError} />
                ))}
              </div>
              <div>
                <Button type="submit" disabled={passwordBusy || newPassword === ''}>
                  {passwordBusy ? 'Adding...' : 'Add password'}
                </Button>
              </div>
            </form>

            {/* ── Add an SSH key ── */}
            <form onSubmit={(e) => void submitKey(e)} className="flex flex-col gap-4">
              <h3 className="text-sm font-medium">Add an SSH key</h3>
              <p className="text-sm text-muted-foreground">
                SSH keys authenticate git operations over SSH. Register the public half of a key
                pair; the private key never leaves your machine.
              </p>
              <ErrorMessage message={keyFormError} />
              <div className="flex flex-col gap-1.5">
                <Label htmlFor={keyId}>Public key</Label>
                <textarea
                  id={keyId}
                  data-slot="textarea"
                  className={cn(TEXTAREA_CLASSES)}
                  required
                  autoComplete="off"
                  spellCheck={false}
                  rows={3}
                  placeholder="ssh-ed25519 AAAA... you@example.com"
                  value={publicKey}
                  onChange={(e) => setPublicKey(e.target.value)}
                  aria-invalid={(keyErrors.public_key?.length ?? 0) > 0 || undefined}
                  aria-describedby={keyErrors.public_key ? `${keyId}-error` : undefined}
                  disabled={keyBusy}
                />
                {(keyErrors.public_key ?? []).map((fieldError, index) => (
                  <FieldError key={`${fieldError.code}-${index}`} error={fieldError} id={`${keyId}-error`} />
                ))}
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor={keyLabelId}>Label (optional)</Label>
                <Input
                  id={keyLabelId}
                  maxLength={100}
                  autoComplete="off"
                  placeholder="Work laptop"
                  value={keyLabel}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setKeyLabel(e.target.value)}
                  aria-invalid={(keyErrors.label?.length ?? 0) > 0 || undefined}
                  aria-describedby={keyErrors.label ? `${keyLabelId}-error` : undefined}
                  disabled={keyBusy}
                />
                {(keyErrors.label ?? []).map((fieldError, index) => (
                  <FieldError key={`${fieldError.code}-${index}`} error={fieldError} id={`${keyLabelId}-error`} />
                ))}
              </div>
              <div>
                <Button type="submit" disabled={keyBusy || publicKey.trim() === ''}>
                  {keyBusy ? 'Adding...' : 'Add key'}
                </Button>
              </div>
            </form>

            {/* ── Link a provider ── */}
            {providers.length > 0 && (
              <section aria-label="Link a provider" className="flex flex-col gap-3">
                <h3 className="text-sm font-medium">Link a provider</h3>
                <div className="flex flex-wrap gap-2">
                  {providers.map((provider) => (
                    <Button
                      key={provider.id}
                      type="button"
                      variant="outline"
                      disabled={linkBusy}
                      onClick={() => void linkProvider(provider)}
                    >
                      {`Link ${provider.display_name}`}
                    </Button>
                  ))}
                </div>
              </section>
            )}
          </>
        )}
      </CardContent>

      {/* ── Edit ── */}
      <Dialog
        open={editTarget !== null}
        onOpenChange={(open) => {
          if (!open && !editBusy) setEditTarget(null);
        }}
      >
        <DialogContent>
          <form onSubmit={(e) => void submitEdit(e)} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Edit credential</DialogTitle>
              <DialogDescription>
                Rename it, or choose the profile a sign-in with it lands on.
              </DialogDescription>
            </DialogHeader>
            <ErrorMessage message={editError} />
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={editLabelId}>Label</Label>
              <Input
                id={editLabelId}
                maxLength={100}
                autoComplete="off"
                value={editLabel}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setEditLabel(e.target.value)}
                disabled={editBusy}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={editProfileId}>Default profile</Label>
              <select
                id={editProfileId}
                className={SELECT_CLASSES}
                value={editProfile}
                onChange={(e) => setEditProfile(e.target.value)}
                disabled={editBusy}
              >
                <option value="">No default</option>
                {profiles.map((profile) => (
                  <option key={profile.uuid} value={profile.uuid}>
                    {profileLabel(profile)}
                  </option>
                ))}
              </select>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setEditTarget(null)}
                disabled={editBusy}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={editBusy}>
                {editBusy ? 'Saving...' : 'Save'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* ── Revoke confirmation ── */}
      <Dialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open && !revoking) setRevokeTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Revoke credential?</DialogTitle>
            <DialogDescription>
              It stops working at once. Sessions that signed in with it end too, which may
              sign you out of this one.
            </DialogDescription>
          </DialogHeader>
          {revokeTarget && (
            <p className="text-sm">
              <bdi>{credentialName(revokeTarget)}</bdi>
            </p>
          )}
          <ErrorMessage message={revokeError} />
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
              onClick={() => void confirmRevoke()}
              disabled={revoking}
            >
              {revoking ? 'Revoking...' : 'Revoke credential'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

/** The kind-specific attributes of one credential, never a secret. */
function CredentialDetails({ credential }: { credential: CredentialView }) {
  if (credential.kind === 'ssh_key') {
    // SSH keys show the type and fingerprint so a key can be recognised.
    return (
      <div className="flex flex-col gap-1">
        {credential.key_type && <Badge variant="secondary">{credential.key_type}</Badge>}
        <span className="block break-all font-mono text-xs" title={credential.fingerprint}>
          {credential.fingerprint}
        </span>
      </div>
    );
  }
  if (credential.kind === 'oidc') {
    // A linked provider shows who it is and which email it asserted.
    return (
      <span className="text-sm">
        {credential.provider_id}
        {credential.email_at_link ? ` (${credential.email_at_link})` : ''}
      </span>
    );
  }
  if (credential.kind === 'api_key') {
    return <span className="font-mono text-xs">{credential.client_id}</span>;
  }
  // Password, emailed code, guest device and kinds not known yet carry nothing
  // more worth showing than the date they were added.
  return (
    <span className="text-sm text-muted-foreground">
      Added {formatDate(credential.created_at)}
    </span>
  );
}
