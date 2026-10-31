/**
 * Builds an unsigned, JWT-shaped access token for tests. The GUI decodes the
 * payload (never verifies it) to read the `act` claim, and treats a stored
 * token whose payload does not decode as possibly impersonating (fail closed),
 * so a fixture that must read as an ordinary session needs a decodable payload.
 * `label` keeps tokens distinguishable in assertions.
 */
export function jwtFixture(label: string, claims: Record<string, unknown> = {}): string {
  const b64url = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return `${b64url({ alg: 'none', typ: 'JWT' })}.${b64url({ sub: label, ...claims })}.sig`;
}
