'use client';

import { useState } from 'react';
import Link from 'next/link';
import { api, ApiRequestError } from '@/lib/api';
import { ErrorMessage } from '@/components/error-message';
import { Button, Input, Label, Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@moduleforge/core-gui';

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitted, setSubmitted] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await api.auth.forgotPassword({ email });
      setSubmitted(true);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        console.error('[forgot-password]', err);
        setError('Something went wrong. Check the browser console for details.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  if (submitted) {
    return (
      <div className="flex min-h-full items-center justify-center p-6">
        <Card className="w-full max-w-sm">
          <CardHeader>
            <CardTitle>Check your email</CardTitle>
            <CardDescription>
              If an account exists for{' '}
              <span className="font-medium text-foreground">{email}</span>, you
              will receive a password reset link shortly.
            </CardDescription>
          </CardHeader>
          <CardFooter>
            <Link href="/auth/login" className="text-sm text-muted-foreground hover:text-foreground">
              Back to sign in
            </Link>
          </CardFooter>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Forgot your password?</CardTitle>
          <CardDescription>
            Enter your email and we will send you a reset link.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <ErrorMessage message={error} />
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
              />
            </div>
            <Button type="submit" className="w-full" disabled={isSubmitting}>
              {isSubmitting ? 'Sending...' : 'Send reset link'}
            </Button>
          </form>
        </CardContent>
        <CardFooter>
          <Link href="/auth/login" className="text-sm text-muted-foreground hover:text-foreground">
            Back to sign in
          </Link>
        </CardFooter>
      </Card>
    </div>
  );
}
