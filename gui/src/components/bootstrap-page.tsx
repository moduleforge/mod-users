'use client';

import { useEffect, useState } from 'react';
import {
  Button,
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
  Input,
  Label,
  ORGANIZATION_LEGAL_FORMS,
  type OrganizationLegalForm,
} from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import {
  api,
  ApiRequestError,
  USERS_ERROR_CODES,
  type BootstrapRequest,
  type TokenResponse,
} from '../lib/api';
import { USERS_GUI_ROUTES } from '../lib/routes';

export interface BootstrapPageProps {
  /**
   * Called once the bootstrap succeeded and the tab holds the new
   * administrator's session (the client stored the token response, and an
   * `AuthProvider` mounted above follows it). Apps SHOULD perform a full-page
   * navigation to the admin landing page here: `OidcSetupGate` probes status
   * only on mount, so a soft navigation would leave it in the stale "setup
   * pending" state.
   */
  onComplete?: (response: TokenResponse) => void;
  /**
   * Called when the operator follows the "Go to sign in" link from the
   * already-set-up state. When omitted the link navigates to
   * `USERS_GUI_ROUTES.login` itself.
   */
  onNavigateToLogin?: () => void;
}

type PageState =
  | { kind: 'loading' }
  | { kind: 'ready' }
  | { kind: 'already-bootstrapped' }
  | { kind: 'status-error'; message: string };

type OperatorKind = 'organization' | 'natural_person';

/**
 * `/bootstrap` — first-run setup. Collects the setup token (from the server
 * logs, sent as `X-Setup-Token`), who operates the deployment (an
 * organization, or a natural person for a single-person self-host) and the
 * first administrator, then calls `POST /v1/bootstrap`. The `201` token
 * response is committed to this tab by the client, so the tab lands signed in
 * as the new administrator.
 *
 * Setup can be finished in either order with OIDC configuration: the setup
 * token stays valid here until both are done (OIDC confirmed here is not
 * required first). Public page: renders without an `AuthProvider`.
 */
export function BootstrapPage({ onComplete, onNavigateToLogin }: BootstrapPageProps = {}) {
  const [state, setState] = useState<PageState>({ kind: 'loading' });
  const [setupToken, setSetupToken] = useState('');
  const [operatorKind, setOperatorKind] = useState<OperatorKind>('organization');
  const [legalName, setLegalName] = useState('');
  const [legalForm, setLegalForm] = useState<OrganizationLegalForm | ''>('');
  const [jurisdiction, setJurisdiction] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [givenName, setGivenName] = useState('');
  const [familyName, setFamilyName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [done, setDone] = useState(false);

  // Read the bootstrap status once: a bootstrapped deployment has nothing to
  // collect here, so the form must not be offered.
  useEffect(() => {
    let cancelled = false;
    api.bootstrap
      .status()
      .then((s) => {
        if (cancelled) return;
        setState(s.bootstrapped ? { kind: 'already-bootstrapped' } : { kind: 'ready' });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setState({
          kind: 'status-error',
          message:
            err instanceof ApiRequestError ? err.message : 'Could not load setup status.',
        });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    const token = setupToken.trim();
    if (token === '') {
      setError('Enter the setup token from the server logs.');
      return;
    }
    let operator: BootstrapRequest['operator'];
    if (operatorKind === 'organization') {
      // The legal form is a closed list (the browser's `required` does not
      // run for a programmatic submit, and the server rejects anything else).
      if (legalName.trim() === '' || legalForm === '') {
        setError('Enter the organization legal name and choose a legal form.');
        return;
      }
      operator = {
        kind: 'organization',
        legal_name: legalName.trim(),
        legal_form: legalForm,
        // Jurisdiction is optional: omit rather than send an empty string.
        ...(jurisdiction.trim() !== '' ? { jurisdiction: jurisdiction.trim() } : {}),
      };
    } else {
      operator = { kind: 'natural_person' };
    }

    setIsSubmitting(true);
    try {
      const response = await api.bootstrap.bootstrap(
        {
          operator,
          admin: {
            email: email.trim(),
            password,
            given_name: givenName.trim(),
            family_name: familyName.trim(),
          },
        },
        token,
      );
      // The client has committed the session. Drop the secrets from state
      // before handing off: neither is needed again.
      setSetupToken('');
      setPassword('');
      setDone(true);
      onComplete?.(response);
    } catch (err) {
      if (err instanceof ApiRequestError && err.code === USERS_ERROR_CODES.alreadyBootstrapped) {
        // Someone (possibly another tab) finished the bootstrap first. The
        // form can never succeed now, so replace it with the dead-end notice.
        setState({ kind: 'already-bootstrapped' });
      } else if (
        err instanceof ApiRequestError &&
        err.code === USERS_ERROR_CODES.setupTokenInvalid
      ) {
        // Wrong, mistyped, or no-longer-active token: keep the rest of the
        // form so the operator only retypes the token.
        setError(
          'The setup token is not valid. Copy it from the server logs (it changes when the server restarts) and try again.',
        );
      } else if (err instanceof ApiRequestError) {
        // Prefer the per-field details (weak password, bad email, ...).
        const details = (err.details ?? [])
          .map((d) => d.message)
          .filter((m) => m.length > 0);
        setError(details.length > 0 ? details.join(' ') : err.message);
      } else {
        console.error('[bootstrap]', err);
        setError('Something went wrong. Check the browser console for details.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  if (state.kind === 'loading') {
    return (
      <Shell>
        <p className="text-sm text-muted-foreground">Loading setup status...</p>
      </Shell>
    );
  }

  if (state.kind === 'status-error') {
    return (
      <Shell>
        <Card className="w-full max-w-lg">
          <CardHeader>
            <CardTitle>Set up this deployment</CardTitle>
            <CardDescription>Could not load setup status.</CardDescription>
          </CardHeader>
          <CardContent>
            <ErrorMessage message={state.message} />
          </CardContent>
        </Card>
      </Shell>
    );
  }

  if (state.kind === 'already-bootstrapped') {
    return (
      <Shell>
        <Card className="w-full max-w-lg">
          <CardHeader>
            <CardTitle>Already set up</CardTitle>
            <CardDescription>
              This deployment already has an operator. Sign in to continue.
            </CardDescription>
          </CardHeader>
          <CardFooter>
            <a
              href={USERS_GUI_ROUTES.login}
              className="text-sm font-medium underline underline-offset-4"
              onClick={(e) => {
                // A host with a client-side router handles the navigation.
                if (onNavigateToLogin) {
                  e.preventDefault();
                  onNavigateToLogin();
                }
              }}
            >
              Go to sign in
            </a>
          </CardFooter>
        </Card>
      </Shell>
    );
  }

  if (done) {
    return (
      <Shell>
        <Card className="w-full max-w-lg">
          <CardHeader>
            <CardTitle>Setup complete</CardTitle>
            <CardDescription>Signing you in as the administrator...</CardDescription>
          </CardHeader>
        </Card>
      </Shell>
    );
  }

  return (
    <Shell>
      <Card className="w-full max-w-lg">
        <CardHeader>
          <CardTitle>Set up this deployment</CardTitle>
          <CardDescription>
            Create the operator and its first administrator. You need the setup
            token printed in the server logs.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <ErrorMessage message={error} />

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="bootstrap-setup-token">Setup token</Label>
              <Input
                id="bootstrap-setup-token"
                type="password"
                required
                autoComplete="off"
                spellCheck={false}
                value={setupToken}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setSetupToken(e.target.value)}
                placeholder="hex token from server logs"
              />
            </div>

            <fieldset className="flex flex-col gap-2">
              <legend className="text-sm font-medium">Who operates this deployment?</legend>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="bootstrap-operator-kind"
                  value="organization"
                  checked={operatorKind === 'organization'}
                  onChange={() => setOperatorKind('organization')}
                />
                An organization
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="bootstrap-operator-kind"
                  value="natural_person"
                  checked={operatorKind === 'natural_person'}
                  onChange={() => setOperatorKind('natural_person')}
                />
                A single person (self-hosted)
              </label>
            </fieldset>

            {/* Organization details apply only to the organization operator;
                a natural-person operator has no legal name or form to give. */}
            {operatorKind === 'organization' && (
              <>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="bootstrap-legal-name">Legal name</Label>
                  <Input
                    id="bootstrap-legal-name"
                    type="text"
                    autoComplete="organization"
                    required
                    value={legalName}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => setLegalName(e.target.value)}
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="bootstrap-legal-form">Legal form</Label>
                  <select
                    id="bootstrap-legal-form"
                    className="border-input flex h-9 w-full rounded-lg border bg-transparent px-3 py-1 text-base shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 md:text-sm"
                    value={legalForm}
                    required
                    onChange={(e) => setLegalForm(e.target.value as OrganizationLegalForm | '')}
                  >
                    <option value="" disabled>
                      Select a legal form
                    </option>
                    {ORGANIZATION_LEGAL_FORMS.map((form) => (
                      <option key={form} value={form}>
                        {form}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="bootstrap-jurisdiction">Jurisdiction (optional)</Label>
                  <Input
                    id="bootstrap-jurisdiction"
                    type="text"
                    value={jurisdiction}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => setJurisdiction(e.target.value)}
                    placeholder="e.g. Delaware, USA"
                  />
                </div>
              </>
            )}

            <div className="grid grid-cols-2 gap-3">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="bootstrap-given-name">First name</Label>
                <Input
                  id="bootstrap-given-name"
                  type="text"
                  autoComplete="given-name"
                  required
                  value={givenName}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setGivenName(e.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="bootstrap-family-name">Last name</Label>
                <Input
                  id="bootstrap-family-name"
                  type="text"
                  autoComplete="family-name"
                  required
                  value={familyName}
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => setFamilyName(e.target.value)}
                />
              </div>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="bootstrap-email">Administrator email</Label>
              <Input
                id="bootstrap-email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setEmail(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="bootstrap-password">Password</Label>
              <Input
                id="bootstrap-password"
                type="password"
                autoComplete="new-password"
                required
                value={password}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setPassword(e.target.value)}
              />
            </div>

            <Button type="submit" className="w-full" disabled={isSubmitting}>
              {isSubmitting ? 'Setting up...' : 'Create administrator'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </Shell>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center p-6">{children}</div>
  );
}
