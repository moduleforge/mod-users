import { ErrorBanner } from '@moduleforge/core-gui';

interface ErrorMessageProps {
  message: string | null;
}

/**
 * Thin wrapper around `@moduleforge/core-gui`'s `<ErrorBanner>` widget,
 * preserving the existing `ErrorMessage` call-site contract: renders
 * nothing when `message` is null, otherwise renders a destructive banner
 * with the message.
 */
export function ErrorMessage({ message }: ErrorMessageProps) {
  return <ErrorBanner error={message} />;
}
