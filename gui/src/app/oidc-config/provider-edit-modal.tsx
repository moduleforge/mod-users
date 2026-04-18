'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Copy, Eye, EyeOff } from 'lucide-react';
import { ApiRequestError } from '@/lib/api';
import {
  fetchOIDCProvider,
  revertOIDCProvider,
  updateOIDCProvider,
  type OIDCProviderAuth,
  type OIDCProviderView,
  type OIDCProviderWriteBody,
} from '@/lib/oidc-provider';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { ErrorMessage } from '@/components/error-message';

interface ProviderEditModalProps {
  providerId: string | null;
  auth: OIDCProviderAuth | null;
  onClose: () => void;
  /** Called after a successful save or revert so the parent can refetch. */
  onSaved: () => void;
}

/** Internal form state — each field is the user-visible string. */
interface FormState {
  displayName: string;
  issuerUrl: string;
  clientId: string;
  clientSecret: string;
  /** Sentinel: true when the user explicitly clicked "Clear secret" so on
   *  save we send `""` rather than omitting the field. */
  clearSecret: boolean;
  claimStyle: string;
  /** Comma-joined; parsed into string[] on save. */
  scopes: string;
  enabled: boolean;
}

function emptyForm(): FormState {
  return {
    displayName: '',
    issuerUrl: '',
    clientId: '',
    clientSecret: '',
    clearSecret: false,
    claimStyle: '',
    scopes: '',
    enabled: true,
  };
}

function viewToForm(view: OIDCProviderView): FormState {
  return {
    displayName: view.display_name ?? '',
    issuerUrl: view.issuer_url ?? '',
    clientId: view.client_id ?? '',
    clientSecret: '',
    clearSecret: false,
    claimStyle: view.claim_style ?? '',
    scopes: view.scopes ? view.scopes.join(', ') : '',
    enabled: view.enabled,
  };
}

/**
 * Parses a comma-separated scope string into a trimmed, empty-filtered
 * array. Empty input → `null` so the PUT body clears the override.
 */
function parseScopes(input: string): string[] | null {
  const parts = input
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
  return parts.length > 0 ? parts : null;
}

/**
 * Emits the JSON body for PUT:
 *  - empty text → `null` (clear override)
 *  - non-empty  → that value
 * Secret is handled separately because the "absent" case must suppress
 * the field entirely (can't express with null/"" which both mean clear).
 */
function buildWriteBody(form: FormState): OIDCProviderWriteBody {
  const body: OIDCProviderWriteBody = {
    display_name: form.displayName.trim() === '' ? null : form.displayName.trim(),
    issuer_url: form.issuerUrl.trim() === '' ? null : form.issuerUrl.trim(),
    client_id: form.clientId.trim() === '' ? null : form.clientId.trim(),
    claim_style: form.claimStyle.trim() === '' ? null : form.claimStyle.trim(),
    scopes: parseScopes(form.scopes),
    enabled: form.enabled,
  };
  if (form.clientSecret !== '') {
    body.client_secret = form.clientSecret;
  } else if (form.clearSecret) {
    body.client_secret = '';
  }
  return body;
}

export function ProviderEditModal({
  providerId,
  auth,
  onClose,
  onSaved,
}: ProviderEditModalProps) {
  const open = providerId !== null && auth !== null;

  const [view, setView] = useState<OIDCProviderView | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isReverting, setIsReverting] = useState(false);
  const [showSecret, setShowSecret] = useState(false);
  const [copied, setCopied] = useState(false);

  // Load the provider view whenever the modal opens for a new provider.
  useEffect(() => {
    if (!open || !providerId || !auth) {
      setView(null);
      setForm(emptyForm());
      setLoadError(null);
      setFormError(null);
      setShowSecret(false);
      setCopied(false);
      return;
    }
    let cancelled = false;
    setIsLoading(true);
    setLoadError(null);
    setFormError(null);
    fetchOIDCProvider(providerId, auth)
      .then((v) => {
        if (cancelled) return;
        setView(v);
        setForm(viewToForm(v));
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiRequestError) {
          setLoadError(err.message);
        } else {
          setLoadError('Could not load provider.');
        }
      })
      .finally(() => {
        if (cancelled) return;
        setIsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, providerId, auth]);

  const secretPlaceholder = useMemo(() => {
    if (!view) return '';
    return view.has_client_secret ? '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022' : '(not set)';
  }, [view]);

  const handleSave = useCallback(async () => {
    if (!providerId || !auth) return;
    setFormError(null);
    setIsSaving(true);
    try {
      const updated = await updateOIDCProvider(
        providerId,
        buildWriteBody(form),
        auth,
      );
      setView(updated);
      setForm(viewToForm(updated));
      onSaved();
      onClose();
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        setFormError('Could not save provider.');
      }
    } finally {
      setIsSaving(false);
    }
  }, [providerId, auth, form, onSaved, onClose]);

  const handleRevert = useCallback(async () => {
    if (!providerId || !auth) return;
    setFormError(null);
    setIsReverting(true);
    try {
      await revertOIDCProvider(providerId, auth);
      onSaved();
      onClose();
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        setFormError('Could not revert provider.');
      }
    } finally {
      setIsReverting(false);
    }
  }, [providerId, auth, onSaved, onClose]);

  const handleCopyCallback = useCallback(async () => {
    if (!view?.callback_url) return;
    try {
      await navigator.clipboard.writeText(view.callback_url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard failures are non-fatal; the field is selectable so the
      // admin can copy manually.
    }
  }, [view]);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            Edit provider{view?.id ? `: ${view.id}` : ''}
          </DialogTitle>
          <DialogDescription>
            Values you enter become overrides. Leaving a field blank falls
            back to the environment or well-known default shown as a hint.
          </DialogDescription>
        </DialogHeader>

        {isLoading && (
          <p className="text-sm text-muted-foreground">Loading provider...</p>
        )}
        {loadError && <ErrorMessage message={loadError} />}

        {view && !loadError && (
          <div className="flex flex-col gap-3">
            <ErrorMessage message={formError} />

            <FieldRow
              id="display-name"
              label="Display name"
              value={form.displayName}
              placeholder={view.display_name_default ?? ''}
              onChange={(v) => setForm((f) => ({ ...f, displayName: v }))}
            />

            <FieldRow
              id="issuer-url"
              label="Issuer URL"
              value={form.issuerUrl}
              placeholder={view.issuer_url_default ?? ''}
              onChange={(v) => setForm((f) => ({ ...f, issuerUrl: v }))}
            />

            <FieldRow
              id="client-id"
              label="Client ID"
              value={form.clientId}
              placeholder={view.client_id_default ?? ''}
              onChange={(v) => setForm((f) => ({ ...f, clientId: v }))}
            />

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="client-secret">Client secret</Label>
              <div className="flex items-center gap-2">
                <Input
                  id="client-secret"
                  type={showSecret ? 'text' : 'password'}
                  autoComplete="off"
                  spellCheck={false}
                  value={form.clientSecret}
                  placeholder={secretPlaceholder}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      clientSecret: e.target.value,
                      clearSecret: false,
                    }))
                  }
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  onClick={() => setShowSecret((s) => !s)}
                  aria-label={showSecret ? 'Hide secret' : 'Show secret'}
                >
                  {showSecret ? <EyeOff /> : <Eye />}
                </Button>
              </div>
              <div className="flex items-center justify-between">
                <p className="text-xs text-muted-foreground">
                  {view.has_client_secret
                    ? 'Secret is set. Leave blank to keep the existing value.'
                    : 'No secret currently stored.'}
                </p>
                {(view.has_client_secret || form.clientSecret !== '') && (
                  <Button
                    type="button"
                    variant="link"
                    size="xs"
                    className="h-auto px-0"
                    onClick={() =>
                      setForm((f) => ({
                        ...f,
                        clientSecret: '',
                        clearSecret: true,
                      }))
                    }
                  >
                    Clear secret
                  </Button>
                )}
              </div>
              {form.clearSecret && (
                <p className="text-xs text-destructive">
                  The stored secret will be cleared on save.
                </p>
              )}
            </div>

            <FieldRow
              id="claim-style"
              label="Claim style"
              value={form.claimStyle}
              placeholder={view.claim_style_default ?? ''}
              onChange={(v) => setForm((f) => ({ ...f, claimStyle: v }))}
            />

            <FieldRow
              id="scopes"
              label="Scopes"
              value={form.scopes}
              placeholder={view.scopes_default.join(', ')}
              onChange={(v) => setForm((f) => ({ ...f, scopes: v }))}
              helpText="Comma-separated (e.g. openid, email, profile)."
            />

            <div className="flex items-center justify-between gap-2 py-1">
              <Label htmlFor="provider-enabled" className="text-sm">
                Enabled
              </Label>
              <Switch
                id="provider-enabled"
                checked={form.enabled}
                onCheckedChange={(next) =>
                  setForm((f) => ({ ...f, enabled: next }))
                }
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="callback-url">Callback URL</Label>
              <div className="flex items-center gap-2">
                <Input
                  id="callback-url"
                  readOnly
                  value={view.callback_url}
                  onClick={(e) => e.currentTarget.select()}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  onClick={handleCopyCallback}
                  aria-label="Copy callback URL"
                >
                  <Copy />
                </Button>
              </div>
              {copied && (
                <p className="text-xs text-muted-foreground">Copied.</p>
              )}
            </div>
          </div>
        )}

        <DialogFooter className="sm:justify-between">
          <Button
            type="button"
            variant="destructive"
            onClick={handleRevert}
            disabled={isLoading || isSaving || isReverting || !view}
          >
            {isReverting ? 'Reverting...' : 'Revert'}
          </Button>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={isSaving || isReverting}
            >
              Cancel
            </Button>
            <Button
              type="button"
              onClick={handleSave}
              disabled={isLoading || isSaving || isReverting || !view}
            >
              {isSaving ? 'Saving...' : 'Save'}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface FieldRowProps {
  id: string;
  label: string;
  value: string;
  placeholder: string;
  helpText?: string;
  onChange: (next: string) => void;
}

function FieldRow({
  id,
  label,
  value,
  placeholder,
  helpText,
  onChange,
}: FieldRowProps) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="text"
        autoComplete="off"
        spellCheck={false}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
      {helpText && (
        <p className="text-xs text-muted-foreground">{helpText}</p>
      )}
    </div>
  );
}
