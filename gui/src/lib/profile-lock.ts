import type { LockRemedy, ProfileLock } from './api';

/**
 * Plain-language copy for the lock reasons the users API emits (the
 * `profile_availability` reasons plus `app_login_not_granted`). The reason set
 * is open on the wire, so an unknown reason falls back to generic copy rather
 * than showing a raw code.
 */
const REASON_COPY: Record<string, string> = {
  profile_suspended: 'This profile is suspended.',
  principal_disabled: 'The account behind this profile is disabled.',
  confined: 'This profile is restricted from signing in here.',
  not_held: 'You no longer hold this profile.',
  acr_too_low:
    'Your sign-in is not strong enough for this profile. It needs a higher level of assurance.',
  credential_not_authorized:
    'The credential you signed in with is not authorized for this profile.',
  provider_required:
    'This profile requires you to sign in with a specific identity provider.',
  app_login_not_granted: 'This app has not granted this profile access.',
};

const FALLBACK_REASON_COPY = 'This profile is not available to this session.';

/** The reason as a sentence a person can act on. */
export function describeLockReason(reason: string): string {
  // An unknown (newer-server) reason must not render as a raw code or crash.
  return REASON_COPY[reason] ?? FALLBACK_REASON_COPY;
}

/**
 * Describes a remedy for display. `sign_in_with_provider` additionally gets an
 * action button from `ProfileRemedy`; the other kinds are explanatory only
 * because their fix happens elsewhere (the host's step-up prompt, the sign-in
 * page, or the invitations page).
 */
export function describeRemedy(remedy: LockRemedy | null): string | null {
  // No remedy means the lock has no user-side fix; the reason text suffices.
  if (!remedy) return null;
  switch (remedy.kind) {
    case 'sign_in_with_provider':
      return remedy.provider_id
        ? `Sign in again with ${remedy.provider_id} to use this profile.`
        : 'Sign in again with the required identity provider to use this profile.';
    case 'step_up':
      return 'Confirm your identity again (step-up) to use this profile.';
    case 'sign_in_with_authorized_credential':
      return 'Sign out and sign in again with a credential that is authorized for this profile.';
    case 'accept_invitation':
      return 'Accept the invitation to this profile on the Invitations page.';
    default:
      // A remedy kind this client predates: the reason text still explains the lock.
      return null;
  }
}

/** Convenience for components that hold a whole `ProfileLock`. */
export function describeLock(lock: ProfileLock): {
  reason: string;
  remedy: string | null;
} {
  return {
    reason: describeLockReason(lock.reason),
    remedy: describeRemedy(lock.remedy),
  };
}
