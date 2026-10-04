'use client';

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from 'react';
import {
  api,
  ApiActionRequiredError,
  ApiRequestError,
  type UserAccountSelf,
} from './api';
import {
  DEFAULT_UNAUTHENTICATED_REDIRECT_URL,
  getStoredToken,
  getTokenStorageKey,
  isSafeSitePath,
} from './config';

function storeToken(token: string): void {
  localStorage.setItem(getTokenStorageKey(), token);
}

function removeToken(): void {
  localStorage.removeItem(getTokenStorageKey());
}

interface AuthContextValue {
  token: string | null;
  user: UserAccountSelf | null;
  isLoading: boolean;
  isAdmin: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (
    email: string,
    password: string,
    givenName: string,
    familyName: string,
  ) => Promise<void>;
  logout: () => void;
  setTokenAndUser: (token: string, user: UserAccountSelf) => void;
  /**
   * Finalize an externally-obtained session (e.g., OAuth callback) by
   * storing the token and hydrating the user from `/v1/self`. On an
   * ApiActionRequiredError, keeps the token and navigates without throwing;
   * on any other failure, clears the token and throws so the caller can
   * surface the error.
   */
  completeExternalLogin: (token: string) => Promise<void>;
  refreshUser: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

interface AuthProviderProps {
  children: React.ReactNode;
  /**
   * Called by the provider when it needs to navigate (e.g., after logout).
   * The consumer (Next.js app, React Router app, etc.) injects framework-
   * specific navigation here. Defaults to a no-op so the provider is usable
   * in isolation (stories, tests).
   */
  onNavigate?: (path: string) => void;
  /**
   * Site-relative path `logout()` (and the 401 branch of `refreshUser`)
   * navigates to. Defaults to `'/auth/login'`; an invalid value logs an error
   * and falls back to the default. Note the provider navigates through
   * `onNavigate`, while the fetch-layer 401 handler navigates through
   * `window.location`; apps with a router should set both `loginPath` and
   * `configureUsersApi({ unauthenticatedRedirectUrl })` to their login route.
   */
  loginPath?: string;
}

export function AuthProvider({
  children,
  onNavigate,
  loginPath,
}: AuthProviderProps) {
  const navigate = onNavigate ?? (() => {});
  let resolvedLoginPath = DEFAULT_UNAUTHENTICATED_REDIRECT_URL;
  if (loginPath !== undefined) {
    if (isSafeSitePath(loginPath)) {
      resolvedLoginPath = loginPath;
    } else {
      console.error('[auth] rejected unsafe loginPath', loginPath);
    }
  }
  const [token, setToken] = useState<string | null>(null);
  const [user, setUser] = useState<UserAccountSelf | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const setTokenAndUser = useCallback((newToken: string, newUser: UserAccountSelf) => {
    storeToken(newToken);
    setToken(newToken);
    setUser(newUser);
  }, []);

  const logout = useCallback(() => {
    removeToken();
    setToken(null);
    setUser(null);
    navigate(resolvedLoginPath);
  }, [navigate, resolvedLoginPath]);

  const refreshUser = useCallback(async () => {
    try {
      const self = await api.self.get();
      setUser(self);
    } catch (err) {
      if (err instanceof ApiActionRequiredError) {
        // Action-required: navigate, don't alarm. The session stays intact —
        // do not call logout() — the user just needs to complete an
        // out-of-band step (e.g. email verification) before continuing.
        navigate(err.path);
      } else if (err instanceof ApiRequestError && err.status === 401) {
        logout();
      }
    }
  }, [logout, navigate]);

  // Validate token on mount
  useEffect(() => {
    const storedToken = getStoredToken();
    if (!storedToken) {
      setIsLoading(false);
      return;
    }
    setToken(storedToken);
    api.self
      .get()
      .then((self) => {
        setUser(self);
      })
      .catch((err: unknown) => {
        if (err instanceof ApiActionRequiredError) {
          // Action-required: navigate, don't alarm. Unlike a 401, this keeps
          // the stored token/session intact and routes the already-
          // authenticated user to finish an out-of-band step.
          navigate(err.path);
          return;
        }
        removeToken();
        setToken(null);
      })
      .finally(() => {
        setIsLoading(false);
      });
  }, []);

  const login = useCallback(
    async (email: string, password: string) => {
      const response = await api.auth.login(email, password);
      setTokenAndUser(response.token, response.user);
    },
    [setTokenAndUser],
  );

  const completeExternalLogin = useCallback(
    async (newToken: string) => {
      // Store the token first so the shared `request()` helper in api.ts
      // picks it up via localStorage for the `/v1/self` call below.
      storeToken(newToken);
      try {
        // `skipAuthRedirect` ensures a bad/expired token surfaces as a thrown
        // ApiRequestError instead of the shared helper hard-redirecting to
        // /auth/login before our catch block can run. The return page needs
        // to control that redirect so it can attach an `?error=...` message.
        const self = await api.self.get({ skipAuthRedirect: true });
        setTokenAndUser(newToken, self);
      } catch (err) {
        if (err instanceof ApiActionRequiredError) {
          // Action-required: navigate, don't alarm. The token written above
          // is valid — the session is kept — but an out-of-band step (e.g.
          // email verification) must complete first. `setUser` cannot be
          // called here since the failed `self.get()` returned no
          // `UserAccountSelf`; sync the reactive `token` state to match the
          // token already persisted to localStorage, and do not rethrow so
          // the caller doesn't render this as an error.
          setToken(newToken);
          navigate(err.path);
          return;
        }
        // Bad/expired token or API failure: don't leave a stale token behind.
        removeToken();
        setToken(null);
        setUser(null);
        throw err;
      }
    },
    [setTokenAndUser, navigate],
  );

  const register = useCallback(
    async (
      email: string,
      password: string,
      givenName: string,
      familyName: string,
    ) => {
      await api.auth.register({
        email,
        password,
        given_name: givenName,
        family_name: familyName,
      });
      // Register intentionally returns no session (no token/user) — email
      // verification runs asynchronously and isn't required to sign in.
      // Login has no verification gate, so chain an explicit login call to
      // auto-authenticate, mirroring what a user doing a follow-up login
      // already gets today.
      const loginResponse = await api.auth.login(email, password);
      setTokenAndUser(loginResponse.token, loginResponse.user);
    },
    [setTokenAndUser],
  );

  const value: AuthContextValue = {
    token,
    user,
    isLoading,
    isAdmin: user?.is_admin ?? false,
    login,
    register,
    logout,
    setTokenAndUser,
    completeExternalLogin,
    refreshUser,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}

/**
 * Like {@link useAuth} but returns `null` if no `AuthProvider` is mounted
 * rather than throwing. Used by pages that render in both authenticated
 * and pre-auth contexts — e.g. `/oidc-config`, which is reachable during
 * the initial setup flow (no provider mounted) AND post-confirmation
 * (provider mounted, possibly with an admin session).
 */
export function useOptionalAuth(): AuthContextValue | null {
  return useContext(AuthContext);
}
