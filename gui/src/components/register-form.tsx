'use client';

import { useState } from 'react';
import { Button, Input, Label } from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import { useAuth } from '../lib/auth-context';
import { ApiRequestError } from '../lib/api';
import { readReturnPath } from '../lib/return-path';
import { describeSignInError, finishSignIn } from '../lib/sign-in';

export interface RegisterFormProps {
  /**
   * Called after a successful registration with the effective return path
   * (the `returnPath` prop, else the validated `?<unauthenticatedReturnParam>=`
   * value, else `null`). Callbacks that declare no parameter keep working.
   */
  onSuccess?: (returnPath: string | null) => void;
  /** Site-relative path echoed to `onSuccess`. An explicit value wins over the query param. */
  returnPath?: string;
  /** The app (slug) the new session signs in to; sent as `app` on register. */
  app?: string;
  /**
   * Called instead of `onSuccess` when the new session found no landing
   * profile (`selection_required`). When omitted, `onSuccess` receives the
   * profile-selection route in place of the return path.
   */
  onSelectionRequired?: (selectProfilePath: string, returnPath: string | null) => void;
  /**
   * Namespaces this form's input `id`/`htmlFor` pairs (e.g. `${idPrefix}-email`)
   * so they stay unique when `LoginForm` and `RegisterForm` are mounted at the
   * same time (see `AuthPage`). Defaults to `'register'` so the component
   * still works standalone (Ladle stories, other consumers).
   */
  idPrefix?: string;
}

export function RegisterForm({
  onSuccess,
  returnPath,
  app,
  onSelectionRequired,
  idPrefix = 'register',
}: RegisterFormProps) {
  const { register } = useAuth();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [givenName, setGivenName] = useState('');
  const [familyName, setFamilyName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    // Mirrors the server-side rule enforced in
    // api/internal/service/user_accounts.go (len(*in.Password) < 12), which
    // counts UTF-8 bytes — not JS UTF-16 code units, which `.length` counts
    // and which can undercount multi-byte characters.
    if (new TextEncoder().encode(password).length < 12) {
      setError('Password must be at least 12 characters.');
      return;
    }

    setIsSubmitting(true);
    try {
      const response = await register(
        email,
        password,
        givenName,
        familyName,
        app ? { app } : undefined,
      );
      finishSignIn(response, returnPath ?? readReturnPath(), { onSuccess, onSelectionRequired });
    } catch (err) {
      const known = describeSignInError(err);
      if (known) {
        // A coded refusal such as byo_identity_disabled has dedicated copy.
        setError(known);
      } else if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        console.error('[register]', err);
        setError('Something went wrong. Check the browser console for details.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-4">
      <ErrorMessage message={error} />
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="given-name">First name</Label>
          <Input
            id="given-name"
            type="text"
            autoComplete="given-name"
            required
            value={givenName}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => setGivenName(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="family-name">Last name</Label>
          <Input
            id="family-name"
            type="text"
            autoComplete="family-name"
            required
            value={familyName}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) => setFamilyName(e.target.value)}
          />
        </div>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${idPrefix}-email`}>Email</Label>
        <Input
          id={`${idPrefix}-email`}
          type="email"
          autoComplete="email"
          required
          value={email}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => setEmail(e.target.value)}
          placeholder="you@example.com"
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${idPrefix}-password`}>
          Password{' '}
          <span className="text-muted-foreground font-normal">(min 12 chars)</span>
        </Label>
        <Input
          id={`${idPrefix}-password`}
          type="password"
          autoComplete="new-password"
          required
          minLength={12}
          value={password}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => setPassword(e.target.value)}
        />
      </div>
      <Button type="submit" className="w-full" disabled={isSubmitting}>
        {isSubmitting ? 'Creating account...' : 'Create account'}
      </Button>
    </form>
  );
}
