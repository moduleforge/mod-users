// ─── API client ──────────────────────────────────────────────────────────────
export { createUsersClient, api, API_BASE_URL, ApiRequestError, fetchProviders } from './lib/api';
export type {
  UsersClient,
  UsersClientOptions,
  ApiError,
  ApiErrorResponse,
  RequestOptions,
  LoginResponse,
  OIDCProvider,
  RegisterRequest,
  EmailCodeRequest,
  EmailCodeVerifyRequest,
  ForgotPasswordRequest,
  ResetPasswordRequest,
  UserAccountSelf,
  UserAccount,
  UserAccountListResponse,
  UpdateProfileRequest,
  AuditEntry,
  AuditListResponse,
  App,
  AppListResponse,
  AppMember,
  AppMembersResponse,
  CreateAppRequest,
  AddAppMemberRequest,
} from './lib/api';

// ─── Auth context ─────────────────────────────────────────────────────────────
export { AuthProvider, useAuth, useOptionalAuth } from './lib/auth-context';

// ─── OIDC config helpers ──────────────────────────────────────────────────────
export {
  fetchOIDCStatus,
  postOIDCConfirm,
  fetchOIDCSaved,
} from './lib/oidc-config';
export type {
  OIDCState,
  OIDCProviderStatus,
  OIDCStatus,
  OIDCConfirmRequest,
  OIDCConfirmArgs,
  OIDCSavedConfig,
} from './lib/oidc-config';

// ─── OIDC provider helpers ────────────────────────────────────────────────────
export {
  fetchOIDCProvider,
  updateOIDCProvider,
  createOIDCProvider,
  revertOIDCProvider,
  PROVIDER_ID_PATTERN,
  WELL_KNOWN_HINTS,
  testProviderURL,
} from './lib/oidc-provider';
export type {
  OIDCProviderView,
  OIDCFieldSource,
  OIDCProviderWriteBody,
  OIDCProviderAuth,
  WellKnownHint,
} from './lib/oidc-provider';

// ─── Utilities ────────────────────────────────────────────────────────────────
export { cn } from './lib/utils';

// ─── Components ───────────────────────────────────────────────────────────────
export { ClientLayout } from './components/client-layout';
export type { ClientLayoutProps } from './components/client-layout';
export { RequireAuth } from './components/require-auth';
export { OidcCallbackPage } from './components/oidc-callback-page';
export type { OidcCallbackPageProps } from './components/oidc-callback-page';
export { SidebarNav } from './components/sidebar-nav';
export type { SidebarNavProps } from './components/sidebar-nav';
export { ErrorMessage } from './components/error-message';

// ─── UI primitives ────────────────────────────────────────────────────────────
export { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogOverlay, DialogPortal, DialogTitle, DialogTrigger } from './components/ui/dialog';
export { Switch } from './components/ui/switch';
export { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from './components/ui/table';
export { Separator } from './components/ui/separator';
