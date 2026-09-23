/**
 * The shared artifact store.
 *
 * Agents publish a document here and hand the returned link to a person or
 * to another agent. The link carries a key scoped to one artifact, so it
 * can be forwarded to exactly the people meant to see that one thing.
 *
 * One secret does both jobs. ARTIFACT_SIGNING_KEY verifies publish and list
 * requests, and every read link is a signature minted with it — so holding
 * a link never confers the ability to publish, while everyone who can
 * publish can mint a link for what they published. There is no second
 * credential to distribute, rotate or leak.
 *
 * What this is NOT is identity. A link says someone was given it, never who
 * is using it. When it matters who looked, that is Cloudflare Access, and
 * the two are deliberately not interchangeable.
 */

import { backfill, indexedAmong, recent, record } from "./catalog";
import { INDEX_SCOPE, PAGE_HEADERS, renderIndex } from "./page";
import { b64urlDecode, verifyLink, verifyRequest } from "./token";

export interface Env {
  ARTIFACTS: R2Bucket;
  INDEX: D1Database;
  ARTIFACT_SIGNING_KEY: string;
}

/** Largest artifact accepted, matching the client's own cap. */
const MAX_BYTES = 16 << 20;

/** Where artifacts sit in the bucket, so the prefix can be listed alone. */
const PREFIX = "art/";

/** Media types this store will serve back. An artifact is rendered in a
 * browser, so what may be stored is an allowlist rather than whatever a
 * client happened to send. */
const CONTENT_TYPES = new Set([
  "text/html",
  "text/markdown",
  "text/plain",
  "text/csv",
  "application/json",
  "image/svg+xml",
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "application/pdf",
]);

/**
 * Headers every artifact is served with.
 *
 * no-referrer matters more than it looks: the key travels in the URL, and
 * without this an artifact that loads anything off-origin hands that key
 * to whoever it loaded from. no-store keeps keyed content out of shared
 * caches, and frame-ancestors stops another site embedding an artifact to
 * read what a viewer does with it.
 *
 * Note what is deliberately absent: a CSP sandbox. Artifacts are living
 * pages that use storage and script, so they are served as ordinary
 * documents on an origin that holds nothing else of value. An artifact's
 * own script can read its own key from the URL — inherent to any
 * key-in-URL scheme, and the reason this is not a place for secrets.
 */
const SECURITY_HEADERS: Record<string, string> = {
  "Referrer-Policy": "no-referrer",
  "X-Content-Type-Options": "nosniff",
  "Cache-Control": "private, no-store",
  "Content-Security-Policy": "frame-ancestors 'none'",
};

function text(body: string, status: number): Response {
  return new Response(body, { status, headers: { "Content-Type": "text/plain; charset=utf-8" } });
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", "Cache-Control": "private, no-store" },
  });
}

/** What a publisher said about an artifact, as stored beside it. */
interface ArtifactMeta {
  title: string;
  email: string;
  session: string;
}

/**
 * Decode the metadata a client sent: base64url over UTF-8 JSON. Decoded to
 * bytes and then through TextDecoder, because atob alone yields one Latin-1
 * character per byte and turns any non-ASCII title into mojibake. Anything
 * unreadable is treated as saying nothing, never as a reason to fail.
 */
function decodeMeta(encoded: string | undefined): ArtifactMeta {
  const empty = { title: "", email: "", session: "" };
  if (!encoded) return empty;
  let parsed: Record<string, unknown>;
  try {
    parsed = JSON.parse(new TextDecoder().decode(b64urlDecode(encoded))) as Record<string, unknown>;
  } catch {
    return empty;
  }
  const field = (name: string) => (typeof parsed[name] === "string" ? (parsed[name] as string) : "");
  return { title: field("title"), email: field("email"), session: field("session") };
}

/** An id is what the client minted: base64url of 128 random bits. Checked
 * rather than trusted, so nothing can walk out of the prefix. */
function validID(id: string): boolean {
  return /^[A-Za-z0-9_-]{16,64}$/.test(id);
}

async function publish(request: Request, env: Env, id: string, nowSeconds: number): Promise<Response> {
  const body = await request.arrayBuffer();
  if (body.byteLength > MAX_BYTES) return text(`artifact is larger than ${MAX_BYTES} bytes`, 413);

  const authorized = await verifyRequest(
    env.ARTIFACT_SIGNING_KEY,
    request.headers.get("X-Artifact-Auth"),
    "PUT",
    `/a/${id}`,
    body,
    nowSeconds
  );
  if (!authorized) return text("not authorized to publish", 401);

  const contentType = (request.headers.get("Content-Type") ?? "").split(";")[0].trim();
  if (!CONTENT_TYPES.has(contentType)) return text(`cannot store ${contentType || "an unlabelled body"}`, 415);

  // Stored verbatim: the client already encoded it, and re-encoding a
  // title here would be a second place for it to differ from what list
  // and read report.
  const meta = request.headers.get("X-Artifact-Meta") ?? "";

  await env.ARTIFACTS.put(PREFIX + id, body, {
    httpMetadata: { contentType },
    customMetadata: { meta },
  });

  // Indexed after the body is stored, never before: an index row pointing
  // at no body is a listing that 404s, while a body with no row is only
  // unlisted, and publishing the same id again repairs it. The timestamp is
  // this Worker's, not the client's -- a laptop clock does not get to decide
  // when something was published.
  const said = decodeMeta(meta);
  try {
    await record(env.INDEX, {
      id,
      title: said.title,
      contentType,
      bytes: body.byteLength,
      email: said.email,
      session: said.session,
      at: new Date(nowSeconds * 1000).toISOString(),
    });
  } catch {
    return text("stored, but not indexed; publish it again to index it", 500);
  }

  return json({ id, bytes: body.byteLength });
}

async function read(request: Request, env: Env, id: string, nowSeconds: number): Promise<Response> {
  const token = new URL(request.url).searchParams.get("k") ?? "";
  const claims = await verifyLink(env.ARTIFACT_SIGNING_KEY, token, id, nowSeconds);
  // A bad key and a missing artifact answer identically. Otherwise this
  // endpoint reports which ids exist to anyone willing to ask.
  if (!claims) return text("no such artifact, or the link has expired", 404);

  const object = await env.ARTIFACTS.get(PREFIX + id);
  if (!object) return text("no such artifact, or the link has expired", 404);

  const headers = new Headers(SECURITY_HEADERS);
  headers.set("Content-Type", object.httpMetadata?.contentType ?? "application/octet-stream");
  const meta = object.customMetadata?.meta;
  if (meta) headers.set("X-Artifact-Meta", meta);
  return new Response(object.body, { headers });
}

async function list(request: Request, env: Env, nowSeconds: number): Promise<Response> {
  const authorized = await verifyRequest(
    env.ARTIFACT_SIGNING_KEY,
    request.headers.get("X-Artifact-Auth"),
    "GET",
    "/a",
    new ArrayBuffer(0),
    nowSeconds
  );
  if (!authorized) return text("not authorized to list", 401);

  const params = new URL(request.url).searchParams;
  const limitParam = Number(params.get("limit") ?? "20");
  const limit = Number.isFinite(limitParam) ? Math.min(Math.max(Math.trunc(limitParam), 1), 200) : 20;
  const by = (params.get("by") ?? "").trim();

  const artifacts = (await recent(env.INDEX, limit, by)).map((entry) => ({
    id: entry.id,
    title: entry.title,
    email: entry.created_by,
    session: entry.created_session,
    content_type: entry.content_type,
    published_at: entry.created_at,
    updated_by: entry.updated_by,
    updated_at: entry.updated_at,
    revisions: entry.revisions,
    bytes: entry.bytes,
  }));

  return json({ artifacts });
}

// REINDEX_PAGE is how many bucket keys one /reindex request looks at. A
// Worker request has a fixed CPU and subrequest budget, so a request that
// walked the whole bucket would fail on exactly the store big enough to need
// a backfill; one bounded page per request costs the same at any size.
const REINDEX_PAGE = 100;

/**
 * Fill in index rows for artifacts the index never saw: published before it
 * existed, or stored in a publish whose index write failed. Idempotent, so
 * running it twice indexes nothing the second time.
 *
 * Each request covers one page of the bucket. The body is empty for the first
 * page, or {"cursor": "..."} to carry on; the answer carries the next cursor
 * until the bucket is exhausted. The body is part of what the request is
 * signed over, so a cursor cannot be swapped under a valid signature.
 *
 * What it writes is what the bucket knows -- the metadata the publisher sent
 * and R2's own upload time and size -- so a backfilled artifact reads as one
 * version by whoever published it, which is all that can be recovered after
 * the fact.
 */
async function reindex(request: Request, env: Env, nowSeconds: number): Promise<Response> {
  const body = await request.arrayBuffer();
  const authorized = await verifyRequest(
    env.ARTIFACT_SIGNING_KEY,
    request.headers.get("X-Artifact-Auth"),
    "POST",
    "/reindex",
    body,
    nowSeconds
  );
  if (!authorized) return text("not authorized to reindex", 401);

  let cursor: string | undefined;
  if (body.byteLength > 0) {
    try {
      const parsed = JSON.parse(new TextDecoder().decode(body));
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) throw new Error("body");
      if (parsed.cursor !== undefined && typeof parsed.cursor !== "string") throw new Error("cursor");
      cursor = parsed.cursor || undefined;
    } catch {
      return text('body must be empty or {"cursor": "<string>"}', 400);
    }
  }

  const page = await env.ARTIFACTS.list({
    prefix: PREFIX,
    limit: REINDEX_PAGE,
    include: ["customMetadata", "httpMetadata"],
    cursor,
  });
  const ids = page.objects.map((object) => object.key.slice(PREFIX.length));
  const known = await indexedAmong(env.INDEX, ids);
  let indexed = 0;
  for (const [i, object] of page.objects.entries()) {
    const id = ids[i];
    if (known.has(id)) continue;
    const said = decodeMeta(object.customMetadata?.meta);
    await backfill(env.INDEX, {
      id,
      title: said.title,
      contentType: object.httpMetadata?.contentType ?? "application/octet-stream",
      bytes: object.size,
      email: said.email,
      session: said.session,
      at: object.uploaded.toISOString(),
    });
    indexed += 1;
  }

  const scanned = page.objects.length;
  const next = page.truncated ? page.cursor : undefined;
  return json({ scanned, indexed, skipped: scanned - indexed, ...(next ? { cursor: next } : {}) });
}

/**
 * The browsable index, opened with a key minted for INDEX_SCOPE. Answers
 * exactly as a bad artifact link does when the key is wrong, so the page's
 * existence is not advertised to anyone without one.
 */
async function browse(request: Request, env: Env, nowSeconds: number): Promise<Response> {
  const params = new URL(request.url).searchParams;
  const token = params.get("k") ?? "";
  const claims = await verifyLink(env.ARTIFACT_SIGNING_KEY, token, INDEX_SCOPE, nowSeconds);
  if (!claims) return text("not found", 404);

  const by = (params.get("by") ?? "").trim();
  const entries = await recent(env.INDEX, 500, by);
  // Links on the page expire with the page, so a forwarded copy of it
  // stops opening anything at the same moment the page itself does.
  const html = await renderIndex(entries, env.ARTIFACT_SIGNING_KEY, token, claims.exp, by);
  return new Response(html, { headers: PAGE_HEADERS });
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (!env.ARTIFACT_SIGNING_KEY) return text("artifact store is not configured", 503);

    const url = new URL(request.url);
    const nowSeconds = Math.floor(Date.now() / 1000);

    if (url.pathname === "/" && request.method === "GET") return browse(request, env, nowSeconds);
    if (url.pathname === "/a" && request.method === "GET") return list(request, env, nowSeconds);
    if (url.pathname === "/reindex" && request.method === "POST") return reindex(request, env, nowSeconds);

    if (url.pathname.startsWith("/a/")) {
      const id = url.pathname.slice("/a/".length);
      if (!validID(id)) return text("not an artifact id", 400);
      if (request.method === "PUT") return publish(request, env, id, nowSeconds);
      if (request.method === "GET") return read(request, env, id, nowSeconds);
      return text("method not allowed", 405);
    }

    return text("not found", 404);
  },
} satisfies ExportedHandler<Env>;
