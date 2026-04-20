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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Switch } from '@/components/ui/switch';
import { ErrorMessage } from '@/components/error-message';
import { Button, Input, Label } from '@moduleforge/core-gui';

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

        <form
          className="flex flex-col gap-3"
          autoComplete="off"
          onSubmit={(e) => {
            e.preventDefault();
            // Mirror the Create button's disabled conditions so Enter
            // only fires when the button itself is clickable.
            if (!isSaving && form.id.trim() !== '') {
              void handleSave();
            }
          }}
        >
          <ErrorMessage message={formError} />

          {/* Decoy inputs — see provider-edit-modal for the rationale. */}
          <input
            type="text"
            name="username"
            autoComplete="username"
            tabIndex={-1}
            aria-hidden="true"
            style={{ position: 'absolute', left: '-9999px', width: 1, height: 1, opacity: 0 }}
            readOnly
          />
          <input
            type="password"
            name="password"
            autoComplete="current-password"
            tabIndex={-1}
            aria-hidden="true"
            style={{ position: 'absolute', left: '-9999px', width: 1, height: 1, opacity: 0 }}
            readOnly
          />

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="add-id">ID (slug)</Label>
            <Input
              id="add-id"
              name="oidc_provider_id"
              type="text"
              autoComplete="off"
              spellCheck={false}
              data-lpignore="true"
              data-1p-ignore="true"
              data-bwignore="true"
              data-form-type="other"
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
            name="oidc_provider_display_name"
            label="Display name"
            value={form.displayName}
            placeholder={hint?.display_name ?? ''}
            onChange={(v) => setForm((f) => ({ ...f, displayName: v }))}
          />

          <FieldRow
            id="add-issuer-url"
            name="oidc_provider_issuer_url"
            label="Issuer URL"
            value={form.issuerUrl}
            placeholder={hint?.issuer_url ?? ''}
            onChange={(v) => setForm((f) => ({ ...f, issuerUrl: v }))}
          />

          <FieldRow
            id="add-client-id"
            name="oidc_provider_client_id"
            label="Client ID"
            value={form.clientId}
            placeholder=""
            inputClassName="no-lastpass"
            onChange={(v) => setForm((f) => ({ ...f, clientId: v }))}
          />

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="add-client-secret">Client secret</Label>
            <div className="flex items-center gap-2">
              <Input
                id="add-client-secret"
                name="oidc_provider_client_secret"
                type={showSecret ? 'text' : 'password'}
                autoComplete="new-password"
                spellCheck={false}
                data-lpignore="true"
                data-1p-ignore="true"
                data-bwignore="true"
                data-form-type="other"
                className="no-lastpass"
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
            name="oidc_provider_claim_style"
            label="Claim style"
            value={form.claimStyle}
            placeholder={hint?.claim_style ?? ''}
            inputClassName="no-lastpass"
            onChange={(v) => setForm((f) => ({ ...f, claimStyle: v }))}
          />

          <FieldRow
            id="add-scopes"
            name="oidc_provider_scopes"
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
        </form>

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
  /** Non-credential name attribute — see provider-edit-modal note. */
  name: string;
  label: string;
  value: string;
  placeholder: string;
  helpText?: string;
  /** Extra className passed to the inner Input (see provider-edit-modal). */
  inputClassName?: string;
  onChange: (next: string) => void;
}

function FieldRow({
  id,
  name,
  label,
  value,
  placeholder,
  helpText,
  inputClassName,
  onChange,
}: FieldRowProps) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        name={name}
        type="text"
        autoComplete="off"
        spellCheck={false}
        data-lpignore="true"
        data-1p-ignore="true"
        data-bwignore="true"
        data-form-type="other"
        className={inputClassName}
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
