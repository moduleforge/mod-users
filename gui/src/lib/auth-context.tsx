'use client';

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import {
  api,
  ApiActionRequiredError,
  ApiRequestError,
  setStepUpHandler,
  subscribeSessionEvents,
  USERS_ERROR_CODES,
  type LockRemedy,
  type LoginOptions,
  type ProfileView,
  type SessionEvent,
  type SessionInfo,
  type SessionView,
  type StepUpHandler,
  type TokenResponse,
  type UserView,
} from './api';
import {
  clearStoredSession,
  DEFAULT_UNAUTHENTICATED_REDIRECT_URL,
  getStoredRefreshToken,
  getStoredToken,
  isSafeSitePath,
} from './config';
import { forkFromSibling, startForkResponder } from './fork';
import { clearHandoffVerifier, readHandoffVerifier } from './handoff';

/**
 * True when the browser is already on `path`. `GET /v1/me` itself returns an
 * action-required envelope while e.g. OIDC is unconfirmed, so a provider
 * mounted above the target page (`/oidc-config`) must not navigate to the
 * page it is already on.
 */
function isCurrentPath(path: string): boolean {
  return typeof window !== 'undefined' && window.location.pathname === path;
}

/** A profile the server refused to act as, with why and what would fix it. */
export interface ProfileLockState {
  /** The locked profile, when known. */
  profileUuid: string | null;
  /** The lock reason (for example `profile_suspended`, `acr_too_low`). */
  reason: string;
  /**
   * What would lift the lock, when the profile list carried it. Null when the
   * lock appeared after the last profile list was fetched (after a page reload
   * the list is not available while locked), in which case only the reason is
   * known.
   */
  remedy: LockRemedy | null;
}

/** Optional extras of `register`. */
export interface RegisterOptions {
  /** The app (slug) the new session signs in to. */
  app?: string;
}

export interface AuthContextValue {
  /** This tab's access token (mirrors sessionStorage); null when signed out or awaiting profile selection. */
  token: string | null;
  /** The current session view, or null. */
  session: SessionView | null;
  /** The active profile (the one the access token acts as), or null. */
  profile: ProfileView | null;
  /** The principal's profiles with their availability to this session. */
  profiles: ProfileView[];
  /** The signed-in user record from `GET /v1/me`; null until loaded or when signed out. */
  user: UserView | null;
  isLoading: boolean;
  /** True when the user holds at least one verified email; false when unverified or signed out. */
  emailVerified: boolean;
  /**
   * True after a sign-in that found no landing profile: the tab holds a
   * refresh token but no access token until `selectProfile` is called.
   */
  selectionRequired: boolean;
  /** Set from a `401 users.profile_unavailable`; cleared by the next successful token response. */
  profileLock: ProfileLockState | null;
  /** True while this tab's session is an impersonation. */
  impersonating: boolean;
  /**
   * Epoch milliseconds at which this tab's access token expires, taken from
   * the last committed token response's `expires_in`; null when unknown
   * (signed out, or the tab was reloaded and no token response has arrived
   * since). An impersonation ends at its token's expiry unless renewed, so
   * the impersonation banner counts down to this.
   */
  accessTokenExpiresAt: number | null;
  login: (
    email: string,
    password: string,
    options?: LoginOptions,
  ) => Promise<TokenResponse>;
  register: (
    email: string,
    password: string,
    givenName: string,
    familyName: string,
    options?: RegisterOptions,
  ) => Promise<TokenResponse>;
  /**
   * `POST /v1/auth/email-code/verify`: signs in with an emailed one-time code.
   * Like `login`, the client commits the token response, so the context's
   * session state follows.
   */
  loginWithEmailCode: (
    email: string,
    code: string,
    options?: LoginOptions,
  ) => Promise<TokenResponse>;
  /** Revokes the server session (best effort), clears this tab, and navigates to the login path. */
  logout: () => Promise<void>;
  /**
   * Refresh grant with the chosen profile: lands the tab on `uuid` after a
   * `selection_required` sign-in, or recovers from a profile lock by choosing
   * another profile (the access token itself cannot be used while locked).
   */
  selectProfile: (uuid: string) => Promise<TokenResponse>;
  /** `POST /v1/session/switch`: replaces only this tab's access token and active profile. */
  switchProfile: (uuid: string) => Promise<TokenResponse>;
  /**
   * Exchanges the one-time `#handoff=` code from an OIDC redirect, with the
   * verifier this tab stored when it started the sign-in, in a single
   * `POST /v1/auth/handoff`. Repeat calls with the same code share that one
   * exchange. Fails with `handoff_verifier_missing` when this tab started no
   * sign-in.
   */
  completeHandoff: (code: string) => Promise<TokenResponse>;
  /** Re-reads the session and user record from the server. */
  refreshUser: () => Promise<void>;
  /**
   * Registers the handler that prompts for a step-up when a request answers
   * `409 users.step_up_required`; returns the unregister function. See
   * `StepUpHandler`.
   */
  registerStepUpHandler: (handler: StepUpHandler | null) => () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

interface AuthSnapshot {
  session: SessionView | null;
  profile: ProfileView | null;
  profiles: ProfileView[];
  selectionRequired: boolean;
}

const SIGNED_OUT: AuthSnapshot = {
  session: null,
  profile: null,
  profiles: [],
  selectionRequired: false,
};

function snapshotOf(source: TokenResponse): AuthSnapshot {
  return {
    session: source.session,
    profile: source.profile,
    profiles: source.profiles,
    selectionRequired: source.selection_required,
  };
}

function snapshotOfInfo(info: SessionInfo): AuthSnapshot {
  return {
    session: info.session,
    profile: info.profile,
    profiles: info.profiles,
    selectionRequired: false,
  };
}

export interface AuthProviderProps {
  children: React.ReactNode;
  /**
   * Called by the provider when it needs to navigate (e.g., after logout).
   * The consumer (Next.js app, React Router app, etc.) injects framework-
   * specific navigation here. Defaults to a no-op so the provider is usable
   * in isolation (stories, tests).
   */
  onNavigate?: (path: string) => void;
  /**
   * Site-relative path `logout()` navigates to. Defaults to the login route
   * (`USERS_GUI_ROUTES.login`); an invalid value logs an error and falls back to the
   * default. Note the provider navigates through `onNavigate`, while the
   * request layer's unauthenticated handling goes through
   * `configureUsersApi({ onUnauthenticated | unauthenticatedRedirectUrl })`;
   * apps with a router should set both.
   */
  loginPath?: string;
  /**
   * The step-up prompt, registered while the provider is mounted. Equivalent
   * to calling `registerStepUpHandler` yourself. Memoize it: a new function
   * identity re-registers.
   */
  stepUpHandler?: StepUpHandler;
}

export function AuthProvider({
  children,
  onNavigate,
  loginPath,
  stepUpHandler,
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
  const [snapshot, setSnapshot] = useState<AuthSnapshot>(SIGNED_OUT);
  const [user, setUser] = useState<UserView | null>(null);
  const [profileLock, setProfileLock] = useState<ProfileLockState | null>(null);
  const [accessTokenExpiresAt, setAccessTokenExpiresAt] = useState<
    number | null
  >(null);
  const [isLoading, setIsLoading] = useState(true);

  // Refs let the long-lived event subscription read current values without
  // re-subscribing on every render.
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;
  const snapshotRef = useRef(snapshot);
  snapshotRef.current = snapshot;
  const userSessionRef = useRef<string | null>(null);
  const hydratedRef = useRef(false);
  const handoffsRef = useRef(new Map<string, Promise<TokenResponse>>());

  /**
   * Loads `GET /v1/me`. An action-required answer navigates (the session
   * stays intact); other failures are logged, because a missing user record
   * must not invalidate a session the server just accepted. A 401 has
   * already been handled by the request pipeline.
   */
  const loadUser = useCallback(async () => {
    try {
      setUser(await api.me.get());
    } catch (err) {
      if (err instanceof ApiActionRequiredError) {
        // Action-required: navigate, don't alarm. The user just needs to
        // finish an out-of-band step (e.g. email verification).
        if (!isCurrentPath(err.path)) navigateRef.current(err.path);
      } else if (!(err instanceof ApiRequestError && err.status === 401)) {
        // A 401 is the request pipeline's business (refresh / sign-out).
        console.error('[auth] could not load the user record', err);
      }
    }
  }, []);

  // Mirror tab-session events (token commits, sign-outs, locks) into state.
  useEffect(() => {
    return subscribeSessionEvents((event: SessionEvent) => {
      if (event.type === 'token_response') {
        const next = snapshotOf(event.response);
        setSnapshot(next);
        setToken(getStoredToken());
        // A committed response means the acting profile is usable again.
        setProfileLock(null);
        // A token response without an access token (selection required) has
        // no expiry to track; otherwise the deadline is now plus the lifetime.
        setAccessTokenExpiresAt(
          event.response.access_token
            ? Date.now() + event.response.expires_in * 1000
            : null,
        );
        const sessionUuid = event.response.session?.uuid ?? null;
        if (sessionUuid !== userSessionRef.current) {
          // A different session (sign-in, impersonation start or end) can
          // belong to a different user; a rotation of the same session
          // cannot, so it keeps the loaded record instead of refetching.
          userSessionRef.current = sessionUuid;
          if (event.response.access_token) void loadUser();
          else setUser(null);
        }
      } else if (event.type === 'signed_out') {
        // The request pipeline already cleared storage and ran the configured
        // unauthenticated handler (the single navigation point).
        userSessionRef.current = null;
        setSnapshot(SIGNED_OUT);
        setToken(null);
        setUser(null);
        setProfileLock(null);
        // No token left, so no deadline.
        setAccessTokenExpiresAt(null);
      } else {
        // profile_unavailable: the remedy lives on the profile list entry,
        // which the lock response itself does not carry.
        const known = snapshotRef.current.profiles.find(
          (p) => p.uuid === event.profileUuid,
        );
        setProfileLock({
          profileUuid: event.profileUuid,
          reason: event.reason,
          remedy: known?.lock?.remedy ?? null,
        });
      }
    });
  }, [loadUser]);

  // Offer this tab's access token to sibling tabs that start with none. An
  // impersonation is not offered: the server refuses to fork it.
  useEffect(() => {
    return startForkResponder(() =>
      snapshotRef.current.session?.impersonating ? null : getStoredToken(),
    );
  }, []);

  // Establish the tab's session once on mount.
  useEffect(() => {
    // Guard against a development double-invocation: the work is idempotent
    // but the fork request would otherwise be sent twice.
    if (hydratedRef.current) return;
    hydratedRef.current = true;

    async function hydrate(): Promise<void> {
      if (getStoredToken() || getStoredRefreshToken()) {
        // A reload: the tab's storage survived. `session.get` also repairs an
        // expired access token through the refresh lineage.
        try {
          const info = await api.session.get();
          setSnapshot(snapshotOfInfo(info));
          setToken(getStoredToken());
          const sessionUuid = info.session?.uuid ?? null;
          // A refresh during `session.get` already started the user load for
          // this session through the event subscription; loading again would
          // fetch the same record twice.
          if (userSessionRef.current !== sessionUuid) {
            userSessionRef.current = sessionUuid;
            await loadUser();
          }
        } catch (err) {
          if (err instanceof ApiActionRequiredError) {
            // Action-required: navigate, don't alarm. Keeps the tokens.
            if (!isCurrentPath(err.path)) navigateRef.current(err.path);
          } else if (
            err instanceof ApiRequestError &&
            err.code === USERS_ERROR_CODES.profileUnavailable
          ) {
            // The lock event already populated `profileLock`; the tokens stay
            // so the user can pick another profile or follow the remedy.
            setToken(getStoredToken());
          } else {
            // Any other failure: the stored session is unusable. Drop it so
            // the tab starts clean rather than half-signed-in.
            clearStoredSession();
            setToken(null);
          }
        }
      } else {
        // A new tab: ask sibling tabs for a session to fork. No answer in
        // time leaves the tab signed out. The fork's token response reaches
        // state through the event subscription above.
        await forkFromSibling();
      }
    }

    void hydrate().finally(() => setIsLoading(false));
  }, [loadUser]);

  const login = useCallback(
    (email: string, password: string, options?: LoginOptions) =>
      // The client commits the response; the event subscription updates state.
      api.auth.login(email, password, options),
    [],
  );

  const register = useCallback(
    (
      email: string,
      password: string,
      givenName: string,
      familyName: string,
      options?: RegisterOptions,
    ) =>
      // Register answers 201 with the new session's token response, so there
      // is no follow-up login.
      api.auth.register({
        email,
        password,
        given_name: givenName,
        family_name: familyName,
        // Only send `app` when set: an absent field means "no app", and an
        // explicit undefined must not reach the wire body.
        ...(options?.app ? { app: options.app } : {}),
      }),
    [],
  );

  const loginWithEmailCode = useCallback(
    (email: string, code: string, options?: LoginOptions) =>
      // The client commits the response; the event subscription updates state.
      api.auth.verifyEmailCode({ email, code, ...options }),
    [],
  );

  const logout = useCallback(async () => {
    try {
      await api.session.logout();
    } catch (err) {
      // An unreachable server must not trap the user in a signed-in UI; the
      // local sign-out below still happens.
      console.error('[auth] logout request failed', err);
    }
    clearStoredSession();
    userSessionRef.current = null;
    setSnapshot(SIGNED_OUT);
    setToken(null);
    setUser(null);
    setProfileLock(null);
    setAccessTokenExpiresAt(null);
    navigateRef.current(resolvedLoginPath);
  }, [resolvedLoginPath]);

  const selectProfile = useCallback((uuid: string) => {
    const refreshToken = getStoredRefreshToken();
    if (!refreshToken) {
      // Without this tab's refresh token there is no grant to make.
      return Promise.reject(
        new ApiRequestError(
          'unauthenticated',
          'Sign in to choose a profile.',
          401,
        ),
      );
    }
    return api.auth.token({
      grant_type: 'refresh_token',
      refresh_token: refreshToken,
      profile_uuid: uuid,
    });
  }, []);

  const switchProfile = useCallback(
    (uuid: string) => api.session.switch(uuid),
    [],
  );

  const completeHandoff = useCallback((code: string) => {
    // One exchange per code: a double-invoked effect (or a re-render) must not
    // send a second request, which the server would refuse as spent after the
    // first cleared this tab's verifier.
    const existing = handoffsRef.current.get(code);
    if (existing) return existing;
    const exchange = (async () => {
      const verifier = readHandoffVerifier();
      if (!verifier) {
        throw new ApiRequestError(
          USERS_ERROR_CODES.handoffVerifierMissing,
          'This sign-in was not started in this browser tab. Please start again from the sign-in page.',
          0,
        );
      }
      try {
        return await api.auth.handoff(code, verifier);
      } finally {
        // Single use: whatever the outcome, this attempt's verifier is spent.
        clearHandoffVerifier();
      }
    })();
    handoffsRef.current.set(code, exchange);
    return exchange;
  }, []);

  const refreshUser = useCallback(async () => {
    try {
      const info = await api.session.get();
      setSnapshot(snapshotOfInfo(info));
      setToken(getStoredToken());
      await loadUser();
    } catch (err) {
      if (err instanceof ApiActionRequiredError) {
        // Action-required: navigate, don't alarm; the session stays intact.
        if (!isCurrentPath(err.path)) navigateRef.current(err.path);
      } else if (!(err instanceof ApiRequestError && err.status === 401)) {
        // 401s were handled (and signed the tab out) by the request pipeline.
        console.error('[auth] could not refresh the session', err);
      }
    }
  }, [loadUser]);

  const registerStepUpHandler = useCallback(
    (handler: StepUpHandler | null) => setStepUpHandler(handler),
    [],
  );

  useEffect(() => {
    if (!stepUpHandler) return undefined;
    // Registered for the provider's lifetime; the returned cleanup only
    // clears the slot if no one replaced this handler meanwhile.
    return setStepUpHandler(stepUpHandler);
  }, [stepUpHandler]);

  const value: AuthContextValue = useMemo(
    () => ({
      token,
      session: snapshot.session,
      profile: snapshot.profile,
      profiles: snapshot.profiles,
      user,
      isLoading,
      emailVerified: user?.email_verified ?? false,
      selectionRequired: snapshot.selectionRequired,
      profileLock,
      impersonating: snapshot.session?.impersonating ?? false,
      accessTokenExpiresAt,
      login,
      register,
      loginWithEmailCode,
      logout,
      selectProfile,
      switchProfile,
      completeHandoff,
      refreshUser,
      registerStepUpHandler,
    }),
    [
      token,
      snapshot,
      user,
      isLoading,
      profileLock,
      accessTokenExpiresAt,
      login,
      register,
      loginWithEmailCode,
      logout,
      selectProfile,
      switchProfile,
      completeHandoff,
      refreshUser,
      registerStepUpHandler,
    ],
  );

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

/**
 * Registers `handler` as the step-up prompt while the calling component is
 * mounted. The prompt UI (a dialog offering password, emailed code or OIDC)
 * calls `api.session.stepUp.*` and resolves with the fresh access token.
 * Must be used under an `AuthProvider`. Memoize `handler`; a new identity
 * re-registers.
 */
export function useStepUpHandler(handler: StepUpHandler | null): void {
  const { registerStepUpHandler } = useAuth();
  useEffect(() => {
    if (!handler) return undefined;
    return registerStepUpHandler(handler);
  }, [handler, registerStepUpHandler]);
}
