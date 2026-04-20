import { AlertCircle } from 'lucide-react';
import { Alert, AlertDescription } from '@moduleforge/core-gui';

interface ErrorMessageProps {
  message: string | null;
}

export function ErrorMessage({ message }: ErrorMessageProps) {
  if (!message) return null;
  return (
    <Alert variant="destructive">
      <AlertCircle className="size-4" />
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  );
}
