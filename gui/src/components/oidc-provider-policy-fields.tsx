'use client';

import { Input, Label } from '@moduleforge/core-gui';
import { Switch } from './ui/switch';
import {
  OIDC_JIT_MODES,
  OIDC_JIT_MODE_HELP,
  type OIDCJitMode,
} from '../lib/oidc-provider';

/** The account-creation policy slice of the provider modals' form state. */
export interface PolicyFormState {
  /** Party UUID text; blank means a deployment-wide (unbound) provider. */
  partyUuid: string;
  jitMode: OIDCJitMode;
  trustAcr: boolean;
}

interface OidcProviderPolicyFieldsProps {
  /** Prefix for the input ids ("add" / "edit") so both modals stay unique. */
  idPrefix: string;
  value: PolicyFormState;
  onChange: (next: PolicyFormState) => void;
  /** The client-side policy error (managed needs a party), if any. */
  policyError: string | null;
}

/**
 * Party binding, JIT mode and ACR trust of an OIDC provider, shared by the
 * add and edit modals. The explanation under the select follows the chosen
 * mode so the operator sees what an unknown identity will create.
 */
export function OidcProviderPolicyFields({
  idPrefix,
  value,
  onChange,
  policyError,
}: OidcProviderPolicyFieldsProps) {
  const partyId = `${idPrefix}-party-uuid`;
  const jitId = `${idPrefix}-jit-mode`;
  const trustId = `${idPrefix}-trust-acr`;
  return (
    <>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={partyId}>Party UUID</Label>
        <Input
          id={partyId}
          name="oidc_provider_party_uuid"
          type="text"
          autoComplete="off"
          spellCheck={false}
          data-lpignore="true"
          data-1p-ignore="true"
          data-bwignore="true"
          data-form-type="other"
          value={value.partyUuid}
          placeholder="(none: available to the whole deployment)"
          onChange={(e) => onChange({ ...value, partyUuid: e.target.value })}
        />
        <p className="text-xs text-muted-foreground">
          Optional. Binds the provider to one party; required for managed JIT.
        </p>
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={jitId}>JIT mode</Label>
        <select
          id={jitId}
          name="oidc_provider_jit_mode"
          className="border-input flex h-9 w-full rounded-lg border bg-transparent px-3 py-1 text-base shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive md:text-sm"
          value={value.jitMode}
          aria-invalid={policyError ? true : undefined}
          onChange={(e) =>
            onChange({ ...value, jitMode: e.target.value as OIDCJitMode })
          }
        >
          {OIDC_JIT_MODES.map((mode) => (
            <option key={mode} value={mode}>
              {mode}
            </option>
          ))}
        </select>
        <p className="text-xs text-muted-foreground">
          {OIDC_JIT_MODE_HELP[value.jitMode]}
        </p>
        {policyError && (
          <p role="alert" className="text-xs text-destructive">
            {policyError}
          </p>
        )}
      </div>

      <div className="flex items-center justify-between gap-2 py-1">
        <div className="flex flex-col">
          <Label htmlFor={trustId} className="text-sm">
            Trust ACR
          </Label>
          <p className="text-xs text-muted-foreground">
            Count the provider&apos;s asserted authentication class toward the
            session&apos;s assurance level.
          </p>
        </div>
        <Switch
          id={trustId}
          checked={value.trustAcr}
          onCheckedChange={(next) => onChange({ ...value, trustAcr: next })}
        />
      </div>
    </>
  );
}
