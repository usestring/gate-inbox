/**
 * The verifying half of internal/extension/artifacts/token.go.
 *
 * Two implementations of one format is a liability, so the wire format is
 * kept deliberately dull -- base64url, SHA-256, newline-joined canonical
 * strings, no canonical JSON anywhere -- and testdata/vectors.json is
 * minted by the Go test and verified by the TypeScript one, so a change to
 * either side that breaks the other fails a test rather than a deploy.
 */

const ENCODER = new TextEncoder();

/** How far a signed request's timestamp may sit from this worker's clock. */
export const AUTH_SKEW_SECONDS = 300;

export interface Claims {
  id: string;
  exp: number;
}

/**
 * Returns a view onto a plain ArrayBuffer rather than ArrayBufferLike.
 * Uint8Array's usual inferred type widens to include SharedArrayBuffer,
 * which WebCrypto's BufferSource does not accept -- so the buffer is
 * allocated explicitly and filled, which is the same work and the type the
 * crypto calls actually want.
 */
function b64urlDecode(value: string): Uint8Array<ArrayBuffer> {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
  const binary = atob(padded);
  const bytes = new Uint8Array(new ArrayBuffer(binary.length));
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

function b64urlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function hmacKey(secret: string): Promise<CryptoKey> {
  return crypto.subtle.importKey("raw", ENCODER.encode(secret.trim()), { name: "HMAC", hash: "SHA-256" }, false, [
    "sign",
    "verify",
  ]);
}

async function hex(bytes: ArrayBuffer): Promise<string> {
  return [...new Uint8Array(bytes)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

/**
 * Verify a read link's key for one artifact.
 *
 * Returns the claims or null, never a reason: separating "expired" from
 * "forged" tells whoever is probing which half to work on, and the holder
 * of a bad link can do nothing with either answer but ask for a new one.
 */
export async function verifyLink(secret: string, token: string, id: string, nowSeconds: number): Promise<Claims | null> {
  const separator = token.indexOf(".");
  if (separator < 0) return null;
  const payload = token.slice(0, separator);
  const signature = token.slice(separator + 1);

  let provided: Uint8Array<ArrayBuffer>;
  try {
    provided = b64urlDecode(signature);
  } catch {
    return null;
  }

  // crypto.subtle.verify is constant-time, so this leaks nothing about the
  // expected signature.
  const valid = await crypto.subtle.verify("HMAC", await hmacKey(secret), provided, ENCODER.encode(payload));
  if (!valid) return null;

  let claims: Claims;
  try {
    claims = JSON.parse(new TextDecoder().decode(b64urlDecode(payload))) as Claims;
  } catch {
    return null;
  }

  if (typeof claims.id !== "string" || !Number.isFinite(claims.exp)) return null;
  // Scope. Without it, a link to any artifact opens every artifact.
  if (claims.id !== id) return null;
  if (claims.exp <= nowSeconds) return null;
  return claims;
}

/**
 * Verify a signed publish or list request.
 *
 * The signature covers the verb, the path, the timestamp and a digest of
 * the body, so a captured header cannot be replayed onto another artifact,
 * another verb, or the same artifact with different content.
 */
export async function verifyRequest(
  secret: string,
  header: string | null,
  method: string,
  path: string,
  body: ArrayBuffer,
  nowSeconds: number
): Promise<boolean> {
  if (!header) return false;
  const parts = header.split(":");
  if (parts.length !== 3 || parts[0] !== "v1") return false;
  const [, stamp, signature] = parts;

  const at = Number(stamp);
  if (!Number.isFinite(at) || Math.abs(nowSeconds - at) > AUTH_SKEW_SECONDS) return false;

  const digest = await hex(await crypto.subtle.digest("SHA-256", body));
  const canonical = [method, path, stamp, digest].join("\n");

  let provided: Uint8Array<ArrayBuffer>;
  try {
    provided = b64urlDecode(signature);
  } catch {
    return false;
  }
  return crypto.subtle.verify("HMAC", await hmacKey(secret), provided, ENCODER.encode(canonical));
}

/**
 * Mint a read link's key, exactly as the Go client's MintLink does. The
 * index page uses it to hand out a working link per artifact. Same claim
 * order, same encoding, no whitespace: a Worker-minted key for the same
 * artifact and expiry is byte-identical to a Go-minted one, and the
 * vectors test holds both sides to that.
 */
export async function mintLink(secret: string, id: string, expSeconds: number): Promise<string> {
  const payload = b64urlEncode(ENCODER.encode(JSON.stringify({ id, exp: expSeconds })));
  const signature = await crypto.subtle.sign("HMAC", await hmacKey(secret), ENCODER.encode(payload));
  return `${payload}.${b64urlEncode(new Uint8Array(signature))}`;
}

export { b64urlEncode, b64urlDecode };
