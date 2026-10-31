'use client';

import { useId, useState } from 'react';
import {
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
import { useAuth } from '../lib/auth-context';
import { api, type ApiActionRequiredError, type UserView } from '../lib/api';
import { describeFailure, fieldErrorsOf } from '../lib/self-service';

const FIELDS = ['email', 'password', 'given_name', 'family_name'] as const;

export interface GuestUpgradeFormProps {
  /** Called with the upgraded user once `POST /v1/me/upgrade` succeeds. */
  onUpgraded?: (user: UserView) => void;
  /** Called with any action-required error other than a step-up; the host navigates. */
  onActionRequired?: (error: ApiActionRequiredError) => void;
  /** Card heading. Defaults to "Create your account". */
  title?: string;
  /** Card description. */
  description?: string;
}

/**
 * Turns the signed-in guest into a registered user (`POST /v1/me/upgrade`) by
 * giving it an email address, a password and a name. Needs `AuthProvider`; the
 * auth context is refreshed afterwards so `useAuth().user.is_guest` follows.
 * Render it for a guest, or let `CredentialsPanel` do so.
 */
export function GuestUpgradeForm({
  onUpgraded,
  onActionRequired,
  title = 'Create your account',
  description = 'You are using a guest session. Add an email address and a password to keep your account and add other ways to sign in.',
}: GuestUpgradeFormProps) {
  const { refreshUser } = useAuth();
  const baseId = useId();
  const [values, setValues] = useState({
    email: '',
    password: '',
    given_name: '',
    family_name: '',
  });
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, FieldErrorData[]>>({});
  const [done, setDone] = useState(false);

  const complete = FIELDS.every((field) => values[field].trim() !== '');

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!complete || busy) return;
    setBusy(true);
    setFormError(null);
    setFieldErrors({});
    try {
      const user = await api.me.upgrade({
        email: values.email.trim(),
        password: values.password,
        given_name: values.given_name.trim(),
        family_name: values.family_name.trim(),
      });
      setDone(true);
      // The guest flag lives on the user record the context holds.
      await refreshUser();
      onUpgraded?.(user);
    } catch (err) {
      const mapped = fieldErrorsOf(err, FIELDS);
      setFieldErrors(mapped);
      // With no field to attach to (a step-up decline, for instance) the
      // message goes to the form-level banner so the failure is never silent.
      if (Object.keys(mapped).length === 0) {
        setFormError(describeFailure(err, 'guest-upgrade-form', onActionRequired));
      }
    } finally {
      setBusy(false);
    }
  }

  const labels: Record<(typeof FIELDS)[number], string> = {
    email: 'Email',
    password: 'Password',
    given_name: 'Given name',
    family_name: 'Family name',
  };

  return (
    <Card className="w-full">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        {done ? (
          // The account exists; the form has nothing left to ask for.
          <p className="text-sm text-green-700 dark:text-green-400" role="status">
            Your account is created.
          </p>
        ) : (
          <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
            <ErrorMessage message={formError} />
            {FIELDS.map((field) => {
              const id = `${baseId}-${field}`;
              const errors = fieldErrors[field] ?? [];
              return (
                <div key={field} className="flex flex-col gap-1.5">
                  <Label htmlFor={id}>{labels[field]}</Label>
                  <Input
                    id={id}
                    type={field === 'password' ? 'password' : field === 'email' ? 'email' : 'text'}
                    autoComplete={
                      field === 'password'
                        ? 'new-password'
                        : field === 'email'
                          ? 'email'
                          : field === 'given_name'
                            ? 'given-name'
                            : 'family-name'
                    }
                    value={values[field]}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                      setValues((prev) => ({ ...prev, [field]: e.target.value }))
                    }
                    aria-invalid={errors.length > 0 || undefined}
                    aria-describedby={errors.length > 0 ? `${id}-error` : undefined}
                    disabled={busy}
                  />
                  {errors.map((error, index) => (
                    <FieldError key={`${error.code}-${index}`} error={error} id={`${id}-error`} />
                  ))}
                </div>
              );
            })}
            <div>
              <Button type="submit" disabled={busy || !complete}>
                {busy ? 'Creating...' : 'Create account'}
              </Button>
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  );
}
