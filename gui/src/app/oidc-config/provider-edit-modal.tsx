'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Copy, Eye, EyeOff } from 'lucide-react';
import { ApiRequestError } from '@/lib/api';
import {
  fetchOIDCProvider,
  revertOIDCProvider,
  testProviderURL,
  updateOIDCProvider,
  type OIDCFieldSource,
  type OIDCProviderAuth,
  type OIDCProviderView,
  type OIDCProviderWriteBody,
} from '@/lib/oidc-provider';
import { Badge } from '@/components/ui/badge';
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
  // Populate each field with the current effective value so the admin
  // sees what's actually in use (env values, well-known defaults, or DB
  // overrides — whichever wins). Clearing a field and saving writes
  // null for that key, which drops the DB override and lets env /
  // well-known take over again; at that point the grey placeholder
  // shows what will resolve on save.
  return {
    displayName: view.display_name ?? view.display_name_default ?? '',
    issuerUrl: view.issuer_url ?? view.issuer_url_default ?? '',
    clientId: view.client_id ?? view.client_id_default ?? '',
    clientSecret: '',
    clearSecret: false,
    claimStyle: view.claim_style ?? view.claim_style_default ?? '',
    scopes: view.scopes
      ? view.scopes.join(', ')
      : (view.scopes_default ?? []).join(', '),
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
 * Emits the JSON body for PUT. For each field:
 *  - empty text → `null` (clear override)
 *  - value matches the effective default (env value or well-known) →
 *    `null` so the override doesn't needlessly displace whatever layer
 *    is currently the source. Before this rule a Save on an unchanged
 *    env-sourced field flipped every field's Source label to "DB
 *    override"; now env / well-known keep their source as long as the
 *    admin didn't actually type anything different.
 *  - non-empty and different from default → the override value.
 *
 * Secret is handled separately because the "absent" case must suppress
 * the field entirely (can't express with null/"" which both mean clear).
 */
function buildWriteBody(form: FormState, view: OIDCProviderView): OIDCProviderWriteBody {
  const body: OIDCProviderWriteBody = {
    display_name: resolveOverride(form.displayName, view.display_name_default),
    issuer_url: resolveOverride(form.issuerUrl, view.issuer_url_default),
    client_id: resolveOverride(form.clientId, view.client_id_default),
    claim_style: resolveOverride(form.claimStyle, view.claim_style_default),
    scopes: resolveScopesOverride(form.scopes, view.scopes_default),
    enabled: form.enabled,
  };
  if (form.clientSecret !== '') {
    body.client_secret = form.clientSecret;
  } else if (form.clearSecret) {
    body.client_secret = '';
  }
  return body;
}

function resolveOverride(
  formValue: string,
  defaultValue: string | null,
): string | null {
  const trimmed = formValue.trim();
  const def = (defaultValue ?? '').trim();
  if (trimmed === '' || trimmed === def) return null;
  return trimmed;
}

function resolveScopesOverride(
  formValue: string,
  defaultScopes: string[],
): string[] | null {
  const parsed = parseScopes(formValue);
  if (parsed === null) return null;
  if (
    defaultScopes.length === parsed.length &&
    defaultScopes.every((s, i) => s === parsed[i])
  ) {
    return null;
  }
  return parsed;
}

/**
 * Deep-equal for FormState. All primitive fields, so a field-by-field
 * check is straightforward and doesn't depend on object-key ordering.
 */
function formsEqual(a: FormState, b: FormState): boolean {
  return (
    a.displayName === b.displayName &&
    a.issuerUrl === b.issuerUrl &&
    a.clientId === b.clientId &&
    a.clientSecret === b.clientSecret &&
    a.clearSecret === b.clearSecret &&
    a.claimStyle === b.claimStyle &&
    a.scopes === b.scopes &&
    a.enabled === b.enabled
  );
}

/**
 * Converts a source enum + provider ID + field key into the small-text
 * label the modal renders under each input. Env sources include the
 * computed env-var name so the operator knows exactly which variable
 * to edit if they want the default back.
 */
function sourceLabel(
  source: OIDCFieldSource,
  providerID: string,
  field: string,
): string {
  switch (source) {
    case 'db':
      return 'Source: DB override';
    case 'env':
      return `Source: env var (${envVarName(providerID, field)})`;
    case 'well_known':
      return 'Source: well-known default';
    case 'fallback':
      return 'Source: fallback';
    case 'none':
      return 'Source: not set';
  }
}

/** Reconstructs the env var name pattern the config loader recognizes. */
function envVarName(providerID: string, field: string): string {
  return `AUTH_PROVIDER_${providerID.toUpperCase()}_${field.toUpperCase()}`;
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
  // baselineForm is the last-persisted view of the form. Dirty detection
  // compares `form` to this; Save + Test buttons branch on the result.
  // Updated only on a successful load or a successful save — NOT on a
  // failed save, so the admin can resubmit without the canonical DB
  // state having changed.
  const [baselineForm, setBaselineForm] = useState<FormState>(emptyForm);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isReverting, setIsReverting] = useState(false);
  const [showSecret, setShowSecret] = useState(false);
  const [copied, setCopied] = useState(false);

  const isDirty = useMemo(
    () => !formsEqual(form, baselineForm),
    [form, baselineForm],
  );

  // Load the provider view whenever the modal opens for a new provider.
  useEffect(() => {
    if (!open || !providerId || !auth) {
      setView(null);
      setForm(emptyForm());
      setBaselineForm(emptyForm());
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
        const next = viewToForm(v);
        setForm(next);
        setBaselineForm(next);
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

  // Blank placeholder on the secret field so "leave blank to keep"
  // instructions below actually match what the admin sees. The
  // dot-pattern the field used to show read as "there's already a
  // value here" — which is technically accurate (has_client_secret is
  // true) but visually inconsistent with the "type to replace" UX.
  const secretPlaceholder = useMemo(() => {
    if (!view) return '';
    return view.has_client_secret ? '' : '(not set)';
  }, [view]);

  const handleSave = useCallback(async () => {
    if (!providerId || !auth || !view) return;
    setFormError(null);
    setIsSaving(true);
    try {
      const updated = await updateOIDCProvider(
        providerId,
        buildWriteBody(form, view),
        auth,
      );
      setView(updated);
      const next = viewToForm(updated);
      setForm(next);
      // Update baseline ONLY on success — a failed save leaves the
      // admin's in-flight edits on screen and the DB unchanged, so the
      // form IS still dirty against what's persisted.
      setBaselineForm(next);
      onSaved();
      // Don't close the modal — admin usually wants to click "Test
      // configuration" right after saving to verify the new values.
      // Modal stays open until admin hits Cancel / X.
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        setFormError('Could not save provider.');
      }
    } finally {
      setIsSaving(false);
    }
  }, [providerId, auth, form, view, onSaved]);

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
          <div className="flex items-center justify-between gap-2">
            <DialogTitle>
              Edit provider{view?.id ? `: ${view.id}` : ''}
            </DialogTitle>
            {view ? <StatusBadge view={view} /> : null}
          </div>
          <DialogDescription>
            Values you enter become overrides. Leaving a field blank (or
            typing a value that matches the default) falls back to the
            environment variable or well-known default.
          </DialogDescription>
        </DialogHeader>

        {isLoading && (
          <p className="text-sm text-muted-foreground">Loading provider...</p>
        )}
        {loadError && <ErrorMessage message={loadError} />}

        {view && !loadError && (
          <div className="flex flex-col gap-3">
            <ErrorMessage message={formError} />

            {/*
              Decoy inputs: LastPass (and to a lesser extent other
              password managers) attach to the first "username +
              password" pair they find inside a form. Presenting
              invisible decoys first diverts the attachment off our
              real fields. Combined with data-lpignore (which newer
              LastPass versions sometimes ignore) this is the most
              reliable way to keep the autofill icon off the
              real inputs.
            */}
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

            <div className="flex items-center justify-between gap-2 rounded-md border bg-muted/30 px-3 py-2">
              <Label htmlFor="provider-enabled" className="text-sm font-medium">
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

            <FieldRow
              id="display-name"
              name="oidc_provider_display_name"
              label="Display name"
              value={form.displayName}
              placeholder={view.display_name_default ?? ''}
              onChange={(v) => setForm((f) => ({ ...f, displayName: v }))}
              sourceText={sourceLabel(view.display_name_source, view.id, 'display_name')}
            />

            <FieldRow
              id="issuer-url"
              name="oidc_provider_issuer_url"
              label="Issuer URL"
              value={form.issuerUrl}
              placeholder={view.issuer_url_default ?? ''}
              onChange={(v) => setForm((f) => ({ ...f, issuerUrl: v }))}
              sourceText={sourceLabel(view.issuer_url_source, view.id, 'issuer_url')}
            />

            <FieldRow
              id="client-id"
              name="oidc_provider_client_id"
              label="Client ID"
              value={form.clientId}
              placeholder={view.client_id_default ?? ''}
              onChange={(v) => setForm((f) => ({ ...f, clientId: v }))}
              sourceText={sourceLabel(view.client_id_source, view.id, 'client_id')}
            />

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="client-secret">Client secret</Label>
              <div className="flex items-center gap-2">
                <Input
                  id="client-secret"
                  name="oidc_provider_client_secret"
                  type={showSecret ? 'text' : 'password'}
                  // "new-password" is the one autocomplete value that
                  // reliably suppresses autofill on password fields;
                  // combined with vendor-ignore attrs it also keeps
                  // LastPass/1Password/Bitwarden from offering to
                  // save "this password" for the server URL.
                  autoComplete="new-password"
                  spellCheck={false}
                  data-lpignore="true"
                  data-1p-ignore="true"
                  data-bwignore="true"
                  data-form-type="other"
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
              <p className="text-xs italic text-muted-foreground">
                {sourceLabel(view.client_secret_source, view.id, 'client_secret')}
              </p>
            </div>

            <FieldRow
              id="claim-style"
              name="oidc_provider_claim_style"
              label="Claim style"
              value={form.claimStyle}
              placeholder={view.claim_style_default ?? ''}
              onChange={(v) => setForm((f) => ({ ...f, claimStyle: v }))}
              sourceText={sourceLabel(view.claim_style_source, view.id, 'claim_style')}
            />

            <FieldRow
              id="scopes"
              name="oidc_provider_scopes"
              label="Scopes"
              value={form.scopes}
              placeholder={view.scopes_default.join(', ')}
              onChange={(v) => setForm((f) => ({ ...f, scopes: v }))}
              helpText="Comma-separated (e.g. openid, email, profile)."
              sourceText={sourceLabel(view.scopes_source, view.id, 'scopes')}
            />

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
              variant="secondary"
              onClick={() => {
                if (!providerId) return;
                // New tab: the auth flow navigates away from this page,
                // and we want the modal + admin session preserved in
                // the original tab. After the round-trip the banner on
                // /oidc-config (same origin) shows the result — and
                // reminds the admin which tab it is.
                window.open(testProviderURL(providerId), '_blank');
              }}
              // Test exercises what's PERSISTED, so we disable it while
              // there are unsaved edits — otherwise the admin would
              // test the old config despite seeing new values in the
              // form. Save the changes, then Test.
              disabled={isLoading || isSaving || isReverting || !view || !providerId || isDirty}
              title={
                isDirty
                  ? 'Save your changes first — Test uses the persisted configuration.'
                  : 'Exercises the full OIDC round-trip in a new tab and reports success/failure without changing your session.'
              }
            >
              Test configuration
            </Button>
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
              // Save disabled when clean: nothing to persist. Dirty
              // detection compares the live form to baselineForm, and
              // baselineForm only advances on successful save — so a
              // failed save leaves Save enabled for a retry.
              disabled={isLoading || isSaving || isReverting || !view || !isDirty}
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
  /**
   * Explicit non-credential `name` so password managers don't
   * heuristically classify this as a login field. We use a
   * `oidc_provider_*` prefix so the semantics are clear and nothing
   * looks like `username` / `password` / `email`.
   */
  name: string;
  label: string;
  value: string;
  placeholder: string;
  helpText?: string;
  /**
   * Small italic line rendered under the input describing which
   * configuration layer provides the currently-effective value.
   * Caller typically builds this via `sourceLabel(...)`.
   */
  sourceText?: string;
  onChange: (next: string) => void;
}

function FieldRow({
  id,
  name,
  label,
  value,
  placeholder,
  helpText,
  sourceText,
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
        // Autofill managers (LastPass, 1Password, Bitwarden) ignore
        // plain `autoComplete="off"` on text inputs; these
        // vendor-specific attributes are the only reliable opt-out.
        data-lpignore="true"
        data-1p-ignore="true"
        data-bwignore="true"
        data-form-type="other"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
      {helpText && (
        <p className="text-xs text-muted-foreground">{helpText}</p>
      )}
      {sourceText && (
        <p className="text-xs italic text-muted-foreground">{sourceText}</p>
      )}
    </div>
  );
}

/**
 * Small badge next to the DialogTitle echoing the per-provider status
 * already shown on /oidc-config. Gives the admin an at-a-glance "this
 * provider currently inits OK / currently fails" indicator without
 * having to close the modal to check.
 */
function StatusBadge({ view }: { view: OIDCProviderView }) {
  if (view.init_ok) {
    return <Badge variant="default">OK</Badge>;
  }
  return <Badge variant="destructive">Failed</Badge>;
}
