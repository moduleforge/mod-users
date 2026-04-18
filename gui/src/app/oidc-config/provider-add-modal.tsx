'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Eye, EyeOff } from 'lucide-react';
import { ApiRequestError } from '@/lib/api';
import {
  createOIDCProvider,
  PROVIDER_ID_PATTERN,
  WELL_KNOWN_HINTS,
  type OIDCProviderAuth,
  type OIDCProviderWriteBody,
  type WellKnownHint,
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

interface ProviderAddModalProps {
  open: boolean;
  auth: OIDCProviderAuth | null;
  onClose: () => void;
  /** Called after a successful create so the parent can refetch the list. */
  onCreated: () => void;
}

interface FormState {
  id: string;
  displayName: string;
  issuerUrl: string;
  clientId: string;
  clientSecret: string;
  claimStyle: string;
  scopes: string;
  enabled: boolean;
}

function emptyForm(): FormState {
  return {
    id: '',
    displayName: '',
    issuerUrl: '',
    clientId: '',
    clientSecret: '',
    claimStyle: '',
    scopes: '',
    enabled: true,
  };
}

function parseScopes(input: string): string[] | null {
  const parts = input
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
  return parts.length > 0 ? parts : null;
}

/**
 * Build the POST body. For create we still use null to mean "no override,
 * fall through to defaults" — the server applies env/well-known defaults
 * on read time. Empty strings are normalized to null for consistency with
 * the edit path.
 */
function buildCreateBody(form: FormState): OIDCProviderWriteBody {
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
  }
  return body;
}

export function ProviderAddModal({
  open,
  auth,
  onClose,
  onCreated,
}: ProviderAddModalProps) {
  const [form, setForm] = useState<FormState>(emptyForm);
  const [formError, setFormError] = useState<string | null>(null);
  const [idError, setIdError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const [showSecret, setShowSecret] = useState(false);

  // Reset state when the modal is closed so reopening starts fresh.
  useEffect(() => {
    if (!open) {
      setForm(emptyForm());
      setFormError(null);
      setIdError(null);
      setShowSecret(false);
    }
  }, [open]);

  const hint: WellKnownHint | undefined = useMemo(() => {
    const trimmed = form.id.trim().toLowerCase();
    return WELL_KNOWN_HINTS[trimmed];
  }, [form.id]);

  const handleSave = useCallback(async () => {
    if (!auth) return;

    const id = form.id.trim().toLowerCase();
    if (!PROVIDER_ID_PATTERN.test(id)) {
      setIdError(
        'ID must be 2-32 characters, lowercase letters/digits/dashes, no leading or trailing dash.',
      );
      return;
    }
    setIdError(null);
    setFormError(null);
    setIsSaving(true);
    try {
      await createOIDCProvider(id, buildCreateBody(form), auth);
      onCreated();
      onClose();
    } catch (err) {
      if (err instanceof ApiRequestError) {
        if (err.status === 409) {
          setFormError('Provider already exists — use Edit instead.');
        } else {
          setFormError(err.message);
        }
      } else {
        setFormError('Could not create provider.');
      }
    } finally {
      setIsSaving(false);
    }
  }, [auth, form, onCreated, onClose]);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add provider</DialogTitle>
          <DialogDescription>
            Enter a provider slug and any overrides. Fields left blank use
            the environment or well-known default.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <ErrorMessage message={formError} />

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="add-id">ID (slug)</Label>
            <Input
              id="add-id"
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={form.id}
              placeholder="e.g. google, microsoft, authelia"
              onChange={(e) => {
                setForm((f) => ({ ...f, id: e.target.value }));
                setIdError(null);
              }}
            />
            {idError && <p className="text-xs text-destructive">{idError}</p>}
            {hint && !idError && (
              <p className="text-xs text-muted-foreground">
                Known provider. Server will apply well-known defaults for
                blank fields.
              </p>
            )}
          </div>

          <FieldRow
            id="add-display-name"
            label="Display name"
            value={form.displayName}
            placeholder={hint?.display_name ?? ''}
            onChange={(v) => setForm((f) => ({ ...f, displayName: v }))}
          />

          <FieldRow
            id="add-issuer-url"
            label="Issuer URL"
            value={form.issuerUrl}
            placeholder={hint?.issuer_url ?? ''}
            onChange={(v) => setForm((f) => ({ ...f, issuerUrl: v }))}
          />

          <FieldRow
            id="add-client-id"
            label="Client ID"
            value={form.clientId}
            placeholder=""
            onChange={(v) => setForm((f) => ({ ...f, clientId: v }))}
          />

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="add-client-secret">Client secret</Label>
            <div className="flex items-center gap-2">
              <Input
                id="add-client-secret"
                type={showSecret ? 'text' : 'password'}
                autoComplete="off"
                spellCheck={false}
                value={form.clientSecret}
                placeholder="(not set)"
                onChange={(e) =>
                  setForm((f) => ({ ...f, clientSecret: e.target.value }))
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
          </div>

          <FieldRow
            id="add-claim-style"
            label="Claim style"
            value={form.claimStyle}
            placeholder={hint?.claim_style ?? ''}
            onChange={(v) => setForm((f) => ({ ...f, claimStyle: v }))}
          />

          <FieldRow
            id="add-scopes"
            label="Scopes"
            value={form.scopes}
            placeholder={
              hint ? hint.scopes.join(', ') : 'openid, email, profile'
            }
            onChange={(v) => setForm((f) => ({ ...f, scopes: v }))}
            helpText="Comma-separated (e.g. openid, email, profile)."
          />

          <div className="flex items-center justify-between gap-2 py-1">
            <Label htmlFor="add-enabled" className="text-sm">
              Enabled
            </Label>
            <Switch
              id="add-enabled"
              checked={form.enabled}
              onCheckedChange={(next) =>
                setForm((f) => ({ ...f, enabled: next }))
              }
            />
          </div>
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={onClose}
            disabled={isSaving}
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={handleSave}
            disabled={isSaving || form.id.trim() === ''}
          >
            {isSaving ? 'Creating...' : 'Create provider'}
          </Button>
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
