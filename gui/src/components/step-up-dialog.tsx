'use client';

import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { Button, Input, Label } from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from './ui/dialog';
import { useStepUpHandler } from '../lib/auth-context';
import {
  api,
  ApiActionRequiredError,
  ApiRequestError,
  type OIDCProvider,
  type StepUpHandler,
} from '../lib/api';
import { isSafeReturnPath } from '../lib/return-path';
import {
  EMAIL_CODE_LENGTH,
  GENERIC_FAILURE,
  RESEND_COOLDOWN_SECONDS,
  isWebUrl,
} from '../lib/self-service';

export interface StepUpDialogProps {
  /**
   * The OIDC providers offered as a step-up factor. When omitted the dialog
   * lists the deployment's providers (`GET /v1/auth/providers`) the first time
   * it opens.
   */
  providers?: OIDCProvider[];
}

type Mode = 'choose' | 'password' | 'email';

/** Settles the promise the request pipeline is awaiting. */
interface Pending {
  resolve: (accessToken: string) => void;
  reject: (reason: Error) => void;
}

/**
 * The step-up prompt. Mount it once, under `AuthProvider`: it registers itself
 * as the step-up handler, so a `409 users.step_up_required` opens this dialog,
 * and the interrupted request is retried once with the fresh access token the
 * chosen factor returns. Factors: the account password, an emailed code, or an
 * OIDC provider. No step-up token exists anywhere: each factor answers with a
 * fresh access token that the client stores for the tab.
 */
export function StepUpDialog({ providers: providedProviders }: StepUpDialogProps) {
  const baseId = useId();
  const passwordId = `${baseId}-password`;
  const codeId = `${baseId}-code`;

  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<Mode>('choose');
  const [password, setPassword] = useState('');
  const [codeSent, setCodeSent] = useState(false);
  const [code, setCode] = useState('');
  const [cooldown, setCooldown] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fetchedProviders, setFetchedProviders] = useState<OIDCProvider[] | null>(null);

  // The open prompt's promise; null when no prompt is showing.
  const pendingRef = useRef<Pending | null>(null);
  // True once the provider list was requested, so reopening does not refetch.
  const providersLoadedRef = useRef(false);

  const reset = useCallback(() => {
    setMode('choose');
    setPassword('');
    setCodeSent(false);
    setCode('');
    setCooldown(0);
    setBusy(false);
    setError(null);
  }, []);

  /** Ends the prompt without a fresh token; the original request then fails. */
  const cancel = useCallback(() => {
    // The pipeline rethrows the original action-required error when the
    // handler rejects, so the caller sees "step-up required", not this text.
    pendingRef.current?.reject(new Error('step-up cancelled'));
    pendingRef.current = null;
    setOpen(false);
  }, []);

  /** Ends the prompt successfully; the pipeline retries with `accessToken`. */
  const succeed = useCallback((accessToken: string) => {
    pendingRef.current?.resolve(accessToken);
    pendingRef.current = null;
    setOpen(false);
  }, []);

  const handler: StepUpHandler = useCallback(
    () =>
      new Promise<string>((resolve, reject) => {
        // A second prompt while one is open is superseded: the pipeline shares
        // one handler call per 409 burst, so this only guards a host that
        // mounted two dialogs. The older caller gets the original error.
        pendingRef.current?.reject(new Error('step-up superseded'));
        pendingRef.current = { resolve, reject };
        reset();
        setOpen(true);
      }),
    [reset],
  );
  useStepUpHandler(handler);

  // Abandon an open prompt if the dialog unmounts, so no request hangs forever.
  useEffect(
    () => () => {
      pendingRef.current?.reject(new Error('step-up unmounted'));
      pendingRef.current = null;
    },
    [],
  );

  // Load the provider list the first time the prompt opens, unless supplied.
  useEffect(() => {
    if (!open || providedProviders || providersLoadedRef.current) return;
    providersLoadedRef.current = true;
    let live = true;
    void api.auth.providers().then((list) => {
      // Ignore a late answer after unmount.
      if (live) setFetchedProviders(list);
    });
    return () => {
      live = false;
    };
  }, [open, providedProviders]);

  // Resend cooldown ticker.
  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setTimeout(() => setCooldown((value) => value - 1), 1000);
    return () => clearTimeout(timer);
  }, [cooldown]);

  const providers = providedProviders ?? fetchedProviders ?? [];

  async function submitPassword(e: React.FormEvent) {
    e.preventDefault();
    if (password === '' || busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await api.session.stepUp.password(password);
      if (!response.access_token) {
        // A step-up must yield a token to retry with; none means the server
        // misbehaved, which is surfaced rather than retried blindly.
        setError(GENERIC_FAILURE);
        return;
      }
      succeed(response.access_token);
    } catch (err) {
      setError(factorFailure(err, 'That password is wrong.'));
    } finally {
      setBusy(false);
    }
  }

  async function sendCode() {
    setBusy(true);
    setError(null);
    try {
      await api.session.stepUp.requestEmailCode();
      setCodeSent(true);
      setCooldown(RESEND_COOLDOWN_SECONDS);
    } catch (err) {
      setError(
        factorFailure(err, 'There is no email address on this account to send a code to.'),
      );
    } finally {
      setBusy(false);
    }
  }

  async function submitCode(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = code.trim();
    if (trimmed.length !== EMAIL_CODE_LENGTH || busy) return;
    setBusy(true);
    setError(null);
    try {
      const response = await api.session.stepUp.verifyEmailCode(trimmed);
      if (!response.access_token) {
        setError(GENERIC_FAILURE);
        return;
      }
      succeed(response.access_token);
    } catch (err) {
      setError(factorFailure(err, 'That code is wrong or has expired.'));
    } finally {
      setBusy(false);
    }
  }

  async function startProvider(provider: OIDCProvider) {
    setBusy(true);
    setError(null);
    try {
      // Come back to the page the user is on once the provider step-up ends.
      const here = window.location.pathname;
      const { authorize_url } = await api.session.stepUp.startOidc(
        provider.id,
        isSafeReturnPath(here) ? here : undefined,
      );
      if (!isWebUrl(authorize_url)) {
        // Only web schemes may reach the full-page navigation below.
        setError('The sign-in provider returned an address that cannot be opened.');
        setBusy(false);
        return;
      }
      // Full page navigation: the provider callback completes the step-up and
      // redirects to the OIDC return page with `#stepped_up=1&return=<path>`,
      // which refreshes the access token and then routes back to this page, so
      // the interrupted request is not retried by this
      // prompt; the user repeats the action. `busy` stays set while leaving.
      window.location.assign(authorize_url);
    } catch (err) {
      setError(factorFailure(err, 'Could not start the provider sign-in.'));
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // Closing by Escape or the corner button is a cancel; ignored mid-call
        // so a half-finished factor cannot orphan its result.
        if (!next && !busy) cancel();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Confirm it&apos;s you</DialogTitle>
          <DialogDescription>
            This change needs a recent sign-in. Choose how to confirm.
          </DialogDescription>
        </DialogHeader>

        <ErrorMessage message={error} />

        {mode === 'choose' && (
          <div className="flex flex-col gap-2">
            <Button type="button" variant="outline" onClick={() => setMode('password')}>
              Use my password
            </Button>
            <Button type="button" variant="outline" onClick={() => setMode('email')}>
              Email me a code
            </Button>
            {providers.map((provider) => (
              <Button
                key={provider.id}
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => void startProvider(provider)}
              >
                {`Continue with ${provider.display_name}`}
              </Button>
            ))}
          </div>
        )}

        {mode === 'password' && (
          <form onSubmit={(e) => void submitPassword(e)} className="flex flex-col gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor={passwordId}>Password</Label>
              <Input
                id={passwordId}
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setPassword(e.target.value)}
              />
            </div>
            <div className="flex flex-wrap gap-2">
              <Button type="submit" disabled={busy || password === ''}>
                Confirm
              </Button>
              <Button type="button" variant="ghost" onClick={() => setMode('choose')} disabled={busy}>
                Back
              </Button>
            </div>
          </form>
        )}

        {mode === 'email' && !codeSent && (
          <div className="flex flex-wrap gap-2">
            <Button type="button" onClick={() => void sendCode()} disabled={busy}>
              Send code
            </Button>
            <Button type="button" variant="ghost" onClick={() => setMode('choose')} disabled={busy}>
              Back
            </Button>
          </div>
        )}

        {mode === 'email' && codeSent && (
          <form onSubmit={(e) => void submitCode(e)} className="flex flex-col gap-3">
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
            <div className="flex flex-wrap gap-2">
              <Button
                type="submit"
                disabled={busy || code.trim().length !== EMAIL_CODE_LENGTH}
              >
                Verify and continue
              </Button>
              <Button
                type="button"
                variant="outline"
                onClick={() => void sendCode()}
                disabled={busy || cooldown > 0}
              >
                {cooldown > 0 ? `Resend code (${cooldown}s)` : 'Resend code'}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setMode('choose')} disabled={busy}>
                Back
              </Button>
            </div>
          </form>
        )}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={cancel} disabled={busy}>
            Cancel
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Message for a failed factor call. On the step-up routes a wrong password or
 * code answers the same `409 users.step_up_required` action, so that action
 * means "that did not verify", not "prompt again".
 */
function factorFailure(err: unknown, refused: string): string {
  if (err instanceof ApiActionRequiredError) {
    // The step-up routes never re-prompt; this 409 is the refusal itself.
    return refused;
  }
  if (err instanceof ApiRequestError) {
    // A 401 on a step-up route is also a refusal (bad credentials), not an
    // expired session, but the server text is the better explanation.
    return err.message;
  }
  console.error('[step-up-dialog]', err);
  return GENERIC_FAILURE;
}
