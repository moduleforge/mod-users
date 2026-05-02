import type { Story } from '@ladle/react';
import { ErrorMessage } from '../components/error-message';

export const WithMessage: Story = () => (
  <ErrorMessage message="Something went wrong. Please try again." />
);

export const WithLongMessage: Story = () => (
  <ErrorMessage message="Invalid credentials. The email or password you entered does not match any account in our system. Please check your input and try again." />
);

export const NoMessage: Story = () => (
  <ErrorMessage message={null} />
);
