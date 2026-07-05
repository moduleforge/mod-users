'use client';

import { useState } from 'react';
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
} from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import { api, ApiRequestError } from '../lib/api';

export interface ResetPasswordPageProps {
  /**
   * The password-reset token, e.g. read from the URL's query string by the
   * consuming app's own page and passed in. This component never reads the
   * URL or a router itself.
   */
  token: string;
  /** Called after the password is successfully reset. Defaults to a no-op. */
  onSuccess?: () => void;
  /** Called when the user clicks "Back to sign in". Defaults to a no-op. */
  onNavigateToLogin?: () => void;
}

export function ResetPasswordPage({
  token,
  onSuccess,
  onNavigateToLogin,
}: ResetPasswordPageProps) {
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    if (!token) {
      setError('Invalid or missing reset token. Please request a new reset link.');
      return;
    }

    // Mirrors the server-side rule enforced in
    // api/internal/service/user_accounts.go (len(*in.Password) < 12) and
    // api/internal/handlers/auth/reset.go's PasswordResetConfirm handler.
    if (newPassword.length < 12) {
      setError('Password must be at least 12 characters.');
      return;
    }

    if (newPassword !== confirmPassword) {
      setError('Passwords do not match.');
      return;
    }

    setIsSubmitting(true);
    try {
      await api.auth.resetPassword({ token, new_password: newPassword });
      onSuccess?.();
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        console.error('[reset-password]', err);
        setError('Something went wrong. Check the browser console for details.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Reset your password</CardTitle>
          <CardDescription>Enter a new password for your account.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <ErrorMessage message={error} />
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="new-password">
                New password{' '}
                <span className="text-muted-foreground font-normal">(min 12 chars)</span>
              </Label>
              <Input
                id="new-password"
                type="password"
                autoComplete="new-password"
                required
                minLength={12}
                value={newPassword}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setNewPassword(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="confirm-password">Confirm new password</Label>
              <Input
                id="confirm-password"
                type="password"
                autoComplete="new-password"
                required
                value={confirmPassword}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setConfirmPassword(e.target.value)}
              />
            </div>
            <Button type="submit" className="w-full" disabled={isSubmitting}>
              {isSubmitting ? 'Resetting...' : 'Reset password'}
            </Button>
          </form>
        </CardContent>
        <CardFooter className="text-sm text-center">
          <p className="text-muted-foreground">
            Remembered your password?{' '}
            <button
              type="button"
              className="text-foreground hover:underline"
              onClick={() => onNavigateToLogin?.()}
            >
              Back to sign in
            </button>
          </p>
        </CardFooter>
      </Card>
    </div>
  );
}
