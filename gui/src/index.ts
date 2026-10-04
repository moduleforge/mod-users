// ─── API client ──────────────────────────────────────────────────────────────
export { createUsersClient, api, API_BASE_URL, ApiRequestError, ApiActionRequiredError, fetchProviders } from './lib/api';
export type {
  UsersClient,
  UsersClientOptions,
  ApiError,
  ApiErrorResponse,
  FieldErrorData,
  ApiAction,
  ApiActionResponse,
  RequestOptions,
  LoginResponse,
  OIDCProvider,
  RegisterRequest,
  EmailCodePurpose,
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

// ─── Runtime configuration ───────────────────────────────────────────────────
export {
  configureUsersApi,
  resetUsersApiConfig,
  getApiBaseUrl,
  getStoredToken,
  clearStoredToken,
  getTokenStorageKey,
  USERS_TOKEN_KEY,
} from './lib/config';
export type { UsersApiConfig, UnauthenticatedContext } from './lib/config';

// ─── Auth context ─────────────────────────────────────────────────────────────
export { AuthProvider, useAuth, useOptionalAuth } from './lib/auth-context';

// ─── Return-path safety ───────────────────────────────────────────────────────
export { isSafeReturnPath } from './lib/return-path';

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
export { LoginForm } from './components/login-form';
export type { LoginFormProps } from './components/login-form';
export { RegisterForm } from './components/register-form';
export type { RegisterFormProps } from './components/register-form';
export { AuthPage } from './components/auth-page';
export type { AuthPageProps, AuthMode } from './components/auth-page';
export { EmailCodePage } from './components/email-code-page';
export type { EmailCodePageProps } from './components/email-code-page';
export { ForgotPasswordPage } from './components/forgot-password-page';
export type { ForgotPasswordPageProps } from './components/forgot-password-page';
export { ResetPasswordPage } from './components/reset-password-page';
export type { ResetPasswordPageProps } from './components/reset-password-page';
export { VerifyEmailPage } from './components/verify-email-page';
export type { VerifyEmailPageProps } from './components/verify-email-page';

// ─── Standard frontend routes ─────────────────────────────────────────────────
export { USERS_GUI_ROUTES } from './lib/routes';
export type { UsersGuiRoutes } from './lib/routes';

// ─── UI primitives ────────────────────────────────────────────────────────────
export { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogOverlay, DialogPortal, DialogTitle, DialogTrigger } from './components/ui/dialog';
export { Switch } from './components/ui/switch';
export { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from './components/ui/table';
export { Separator } from './components/ui/separator';
