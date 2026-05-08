import type { GlobalProvider } from '@ladle/react';
import '../src/styles.css';

export const Provider: GlobalProvider = ({ children, globalState }) => (
  <div className={globalState.theme === 'dark' ? 'dark' : ''}>
    <div className="min-h-screen bg-background text-foreground p-6">
      {children}
    </div>
  </div>
);
