/**
 * Route tests for the artifact store, against in-memory stand-ins for R2 and
 * D1.
 *
 * These cover the access rules and the index rather than the storage: who
 * may publish, what a link opens, what an unauthenticated caller is allowed
 * to learn, and who the index says made what. The fakes keep them runnable
 * with no wrangler, no miniflare and no network.
 */
import { expect, test, describe, beforeEach } from "bun:test";
import { FakeD1 } from "./fake-d1";
import worker, { type Env } from "./index";
import { INDEX_SCOPE } from "./page";
import { mintLink } from "./token";

const KEY = "test-key-not-a-real-secret";
const ID = "QUJDREVGR0hJSktMTU5PUA";
const encoder = new TextEncoder();

interface Stored {
  body: ArrayBuffer;
  httpMetadata?: { contentType?: string };
  customMetadata?: Record<string, string>;
  uploaded: Date;
  size: number;
}

class FakeBucket {
  objects = new Map<string, Stored>();
  pageSize = 0;

  async put(key: string, body: ArrayBuffer, options: { httpMetadata?: any; customMetadata?: any }) {
    this.objects.set(key, {
      body,
      httpMetadata: options.httpMetadata,
      customMetadata: options.customMetadata,
      uploaded: new Date("2026-09-17T00:00:00Z"),
      size: body.byteLength,
    });
  }

  async get(key: string) {
    const found = this.objects.get(key);
    return found ? { ...found, body: found.body } : null;
  }

  async list({ prefix, cursor, limit }: { prefix: string; cursor?: string; limit?: number }) {
    const all = [...this.objects.entries()]
      .filter(([key]) => key.startsWith(prefix))
      .map(([key, value]) => ({ key, ...value }));
    // pageSize, when set, stands in for a smaller R2 page than the caller
    // asked for, so a test can prove the caller follows the cursor.
    const size = this.pageSize || limit;
    if (!size) return { objects: all, truncated: false };
    const start = cursor ? Number(cursor) : 0;
    const objects = all.slice(start, start + size);
    const next = start + size;
    return { objects, truncated: next < all.length, cursor: String(next) };
  }
}

let env: Env;
let index: FakeD1;
beforeEach(() => {
  index = new FakeD1();
  env = {
    ARTIFACTS: new FakeBucket() as unknown as R2Bucket,
    INDEX: index as unknown as D1Database,
    ARTIFACT_SIGNING_KEY: KEY,
  };
});

function b64url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function mac(message: string): Promise<string> {
  const key = await crypto.subtle.importKey("raw", encoder.encode(KEY), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return b64url(new Uint8Array(await crypto.subtle.sign("HMAC", key, encoder.encode(message))));
}

async function signed(method: string, path: string, body: string): Promise<string> {
  const stamp = Math.floor(Date.now() / 1000).toString();
  const digest = [...new Uint8Array(await crypto.subtle.digest("SHA-256", encoder.encode(body)))]
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
  return `v1:${stamp}:${await mac([method, path, stamp, digest].join("\n"))}`;
}

async function linkFor(id: string, expSeconds: number): Promise<string> {
  const payload = b64url(encoder.encode(JSON.stringify({ id, exp: expSeconds })));
  return `${payload}.${await mac(payload)}`;
}

async function publish(id: string, body: string, contentType = "text/html"): Promise<Response> {
  return worker.fetch(
    new Request(`https://artifacts.test/a/${id}`, {
      method: "PUT",
      body,
      headers: { "Content-Type": contentType, "X-Artifact-Auth": await signed("PUT", `/a/${id}`, body) },
    }),
    env
  );
}

const soon = () => Math.floor(Date.now() / 1000) + 3600;

describe("publishing", () => {
  test("stores a signed artifact", async () => {
    const response = await publish(ID, "<p>hi</p>");
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ id: ID, bytes: 9 });
  });

  test("refuses an unsigned request", async () => {
    const response = await worker.fetch(
      new Request(`https://artifacts.test/a/${ID}`, { method: "PUT", body: "x", headers: { "Content-Type": "text/html" } }),
      env
    );
    expect(response.status).toBe(401);
  });

  test("refuses a signature for a different artifact", async () => {
    const response = await worker.fetch(
      new Request(`https://artifacts.test/a/${ID}`, {
        method: "PUT",
        body: "x",
        headers: { "Content-Type": "text/html", "X-Artifact-Auth": await signed("PUT", "/a/some-other-id", "x") },
      }),
      env
    );
    expect(response.status).toBe(401);
  });

  test("refuses a media type it will not serve", async () => {
    const response = await publish(ID, "#!/bin/sh", "application/x-shellscript");
    expect(response.status).toBe(415);
  });

  test("refuses an id that could walk out of the prefix", async () => {
    const response = await worker.fetch(new Request("https://artifacts.test/a/../secrets", { method: "PUT", body: "x" }), env);
    expect([400, 404]).toContain(response.status);
  });
});

describe("reading", () => {
  test("serves an artifact to a valid link", async () => {
    await publish(ID, "<p>hi</p>");
    const response = await worker.fetch(new Request(`https://artifacts.test/a/${ID}?k=${await linkFor(ID, soon())}`), env);
    expect(response.status).toBe(200);
    expect(await response.text()).toBe("<p>hi</p>");
    expect(response.headers.get("Content-Type")).toBe("text/html");
  });

  test("keeps the key out of referrers and shared caches", async () => {
    await publish(ID, "<p>hi</p>");
    const response = await worker.fetch(new Request(`https://artifacts.test/a/${ID}?k=${await linkFor(ID, soon())}`), env);
    expect(response.headers.get("Referrer-Policy")).toBe("no-referrer");
    expect(response.headers.get("Cache-Control")).toBe("private, no-store");
    expect(response.headers.get("X-Content-Type-Options")).toBe("nosniff");
  });

  test("refuses a link with no key", async () => {
    await publish(ID, "<p>hi</p>");
    const response = await worker.fetch(new Request(`https://artifacts.test/a/${ID}`), env);
    expect(response.status).toBe(404);
  });

  test("refuses a link minted for another artifact", async () => {
    await publish(ID, "<p>hi</p>");
    const other = await linkFor("QkJCQkJCQkJCQkJCQkJCQg", soon());
    const response = await worker.fetch(new Request(`https://artifacts.test/a/${ID}?k=${other}`), env);
    expect(response.status).toBe(404);
  });

  test("refuses an expired link", async () => {
    await publish(ID, "<p>hi</p>");
    const expired = await linkFor(ID, Math.floor(Date.now() / 1000) - 1);
    const response = await worker.fetch(new Request(`https://artifacts.test/a/${ID}?k=${expired}`), env);
    expect(response.status).toBe(404);
  });

  // A wrong key and a missing artifact have to be indistinguishable, or
  // this endpoint reports which ids exist to anyone willing to ask.
  test("does not distinguish a bad key from a missing artifact", async () => {
    const missing = await worker.fetch(new Request(`https://artifacts.test/a/${ID}?k=${await linkFor(ID, soon())}`), env);
    await publish(ID, "<p>hi</p>");
    const badKey = await worker.fetch(new Request(`https://artifacts.test/a/${ID}?k=nonsense.nonsense`), env);
    expect(missing.status).toBe(badKey.status);
    expect(await missing.text()).toBe(await badKey.text());
  });
});

describe("listing", () => {
  test("needs a signature", async () => {
    const response = await worker.fetch(new Request("https://artifacts.test/a"), env);
    expect(response.status).toBe(401);
  });

  // Encoded the way the Go client encodes it: UTF-8 JSON, base64url.
  function metaHeader(meta: Record<string, string>): string {
    return b64url(encoder.encode(JSON.stringify(meta)));
  }

  async function publishWithMeta(id: string, meta: Record<string, string>): Promise<void> {
    const response = await worker.fetch(
      new Request(`https://artifacts.test/a/${id}`, {
        method: "PUT",
        body: "<p>hi</p>",
        headers: {
          "Content-Type": "text/html",
          "X-Artifact-Meta": metaHeader(meta),
          "X-Artifact-Auth": await signed("PUT", `/a/${id}`, "<p>hi</p>"),
        },
      }),
      env
    );
    expect(response.status).toBe(200);
  }

  async function listed(query = ""): Promise<Array<Record<string, unknown>>> {
    const response = await worker.fetch(
      new Request(`https://artifacts.test/a${query}`, { headers: { "X-Artifact-Auth": await signed("GET", "/a", "") } }),
      env
    );
    expect(response.status).toBe(200);
    return ((await response.json()) as { artifacts: Array<Record<string, unknown>> }).artifacts;
  }

  test("reports who published what", async () => {
    await publishWithMeta(ID, { title: "A Report", email: "alice@example.test", session: "session-1" });
    const artifacts = await listed("?limit=10");
    expect(artifacts).toHaveLength(1);
    expect(artifacts[0]).toMatchObject({ id: ID, title: "A Report", email: "alice@example.test", session: "session-1" });
  });

  test("narrows to one publisher, ignoring case", async () => {
    await publishWithMeta(ID, { title: "Mine", email: "Alice@Example.test" });
    await publishWithMeta("QkJCQkJCQkJCQkJCQkJCQg", { title: "Theirs", email: "teammate@example.test" });
    const mine = await listed("?by=alice%40example.test");
    expect(mine.map((artifact) => artifact.title)).toEqual(["Mine"]);
  });

  // atob alone decodes base64 to one Latin-1 character per byte, so a
  // UTF-8 title came back as mojibake in every listing.
  test("keeps a non-ASCII title intact", async () => {
    await publishWithMeta(ID, { title: "Q3 résumé — 数据", email: "alice@example.test" });
    const [artifact] = await listed();
    expect(artifact.title).toBe("Q3 résumé — 数据");
  });

  test("indexes an artifact with unreadable metadata instead of refusing it", async () => {
    const response = await worker.fetch(
      new Request(`https://artifacts.test/a/${ID}`, {
        method: "PUT",
        body: "<p>hi</p>",
        headers: {
          "Content-Type": "text/html",
          "X-Artifact-Meta": "!!not-base64!!",
          "X-Artifact-Auth": await signed("PUT", `/a/${ID}`, "<p>hi</p>"),
        },
      }),
      env
    );
    expect(response.status).toBe(200);
    const [artifact] = await listed();
    expect(artifact).toMatchObject({ id: ID, title: "", email: "" });
  });
});

const OTHER = "QkJCQkJCQkJCQkJCQkJCQg";

async function publishAs(id: string, meta: Record<string, string>, body = "<p>hi</p>"): Promise<Response> {
  return worker.fetch(
    new Request(`https://artifacts.test/a/${id}`, {
      method: "PUT",
      body,
      headers: {
        "Content-Type": "text/html",
        "X-Artifact-Meta": b64url(encoder.encode(JSON.stringify(meta))),
        "X-Artifact-Auth": await signed("PUT", `/a/${id}`, body),
      },
    }),
    env
  );
}

async function listAll(query = ""): Promise<Array<Record<string, unknown>>> {
  const response = await worker.fetch(
    new Request(`https://artifacts.test/a${query}`, { headers: { "X-Artifact-Auth": await signed("GET", "/a", "") } }),
    env
  );
  return ((await response.json()) as { artifacts: Array<Record<string, unknown>> }).artifacts;
}

describe("the index", () => {
  test("keeps who made it when a teammate revises it", async () => {
    await publishAs(ID, { title: "Report", email: "maker@example.test", session: "s1" });
    await publishAs(ID, { title: "Report v2", email: "fixer@example.test", session: "s2" }, "<p>v2</p>");
    const [artifact] = await listAll();
    expect(artifact).toMatchObject({
      title: "Report v2",
      email: "maker@example.test",
      session: "s1",
      updated_by: "fixer@example.test",
      revisions: 2,
    });
  });

  // "By" answers "what did they make". Fixing a typo in a teammate's
  // report does not make it yours.
  test("filters on who made it, not who last touched it", async () => {
    await publishAs(ID, { title: "Theirs", email: "maker@example.test" });
    await publishAs(ID, { title: "Theirs, fixed", email: "fixer@example.test" });
    await publishAs(OTHER, { title: "Mine", email: "fixer@example.test" });
    const made = await listAll("?by=fixer%40example.test");
    expect(made.map((artifact) => artifact.title)).toEqual(["Mine"]);
  });

  test("stamps this Worker's clock, not the publisher's", async () => {
    await publishAs(ID, { title: "T", email: "a@example.test", published_at: "1999-01-01T00:00:00Z" });
    const [artifact] = await listAll();
    expect(String(artifact.published_at).slice(0, 4)).toBe(String(new Date().getUTCFullYear()));
  });

  // A publish that reported success but never reached the index would be
  // a document nobody can find by browsing. The body is kept, so publishing
  // the same id again repairs it.
  test("does not report success when the index write fails, and a republish repairs it", async () => {
    index.failing = true;
    const failed = await publishAs(ID, { title: "Report", email: "a@example.test" });
    expect(failed.status).toBe(500);

    const read = await worker.fetch(new Request(`https://artifacts.test/a/${ID}?k=${await linkFor(ID, soon())}`), env);
    expect(read.status).toBe(200);

    index.failing = false;
    expect((await publishAs(ID, { title: "Report", email: "a@example.test" })).status).toBe(200);
    expect((await listAll()).map((artifact) => artifact.id)).toEqual([ID]);
  });
});

describe("the browsable page", () => {
  async function page(query: string): Promise<Response> {
    return worker.fetch(new Request(`https://artifacts.test/${query}`), env);
  }

  test("opens with an index key and says who made what", async () => {
    await publishAs(ID, { title: "Q3 report", email: "maker@example.test" });
    const response = await page(`?k=${await mintLink(KEY, INDEX_SCOPE, soon())}`);
    expect(response.status).toBe(200);
    const html = await response.text();
    expect(html).toContain("Q3 report");
    expect(html).toContain("maker@example.test");
  });

  // An index key opens everything listed, so the two scopes must never
  // stand in for each other.
  test("is not opened by an artifact key, and an index key opens no artifact", async () => {
    await publishAs(ID, { title: "T" });
    expect((await page(`?k=${await linkFor(ID, soon())}`)).status).toBe(404);
    const indexKey = await mintLink(KEY, INDEX_SCOPE, soon());
    expect((await worker.fetch(new Request(`https://artifacts.test/a/${ID}?k=${indexKey}`), env)).status).toBe(404);
  });

  test("refuses a missing or expired key without saying it exists", async () => {
    expect((await page("")).status).toBe(404);
    const expired = await mintLink(KEY, INDEX_SCOPE, Math.floor(Date.now() / 1000) - 1);
    const response = await page(`?k=${expired}`);
    expect(response.status).toBe(404);
    expect(await response.text()).toBe("not found");
  });

  test("escapes what publishers wrote", async () => {
    await publishAs(ID, { title: `<script>alert(1)</script>`, email: `x"><img src=y>@example.test` });
    const html = await (await page(`?k=${await mintLink(KEY, INDEX_SCOPE, soon())}`)).text();
    expect(html).not.toContain("<script>alert(1)</script>");
    expect(html).not.toContain(`"><img src=y>`);
    expect(html).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
  });

  test("runs no script even if escaping were missed, and keeps its keys out of referrers", async () => {
    const response = await page(`?k=${await mintLink(KEY, INDEX_SCOPE, soon())}`);
    expect(response.headers.get("Content-Security-Policy")).toContain("default-src 'none'");
    expect(response.headers.get("Referrer-Policy")).toBe("no-referrer");
  });

  // Links on a forwarded copy of the page stop working when the page does.
  test("hands out links that open each artifact and expire with the page", async () => {
    await publishAs(ID, { title: "T", email: "a@example.test" });
    const exp = soon();
    const html = await (await page(`?k=${await mintLink(KEY, INDEX_SCOPE, exp)}`)).text();
    const href = html.match(/href="(\/a\/[^"]+)"/)![1].replace(/&amp;/g, "&");
    expect((await worker.fetch(new Request(`https://artifacts.test${href}`), env)).status).toBe(200);

    const key = decodeURIComponent(new URL(`https://x${href}`).searchParams.get("k")!);
    const claims = JSON.parse(atob(key.split(".")[0].replace(/-/g, "+").replace(/_/g, "/")));
    expect(claims.exp).toBe(exp);
  });

  test("narrows to one person", async () => {
    await publishAs(ID, { title: "Mine", email: "me@example.test" });
    await publishAs(OTHER, { title: "Theirs", email: "them@example.test" });
    const html = await (await page(`?k=${await mintLink(KEY, INDEX_SCOPE, soon())}&by=me%40example.test`)).text();
    expect(html).toContain("Mine");
    expect(html).not.toContain("Theirs");
  });
});

describe("reindex", () => {
  async function reindex(cursor?: string): Promise<Response> {
    const body = cursor === undefined ? "" : JSON.stringify({ cursor });
    return worker.fetch(
      new Request("https://artifacts.test/reindex", {
        method: "POST",
        body,
        headers: { "X-Artifact-Auth": await signed("POST", "/reindex", body) },
      }),
      env
    );
  }

  // Everything published before the index existed is in the bucket and
  // nowhere else. Without this it opens by link but lists nowhere.
  test("indexes an artifact the index never saw", async () => {
    await publishAs(ID, { title: "Older", email: "maker@example.test", session: "s1" });
    index.db.exec("DELETE FROM artifacts");
    expect(await listAll()).toHaveLength(0);

    expect(await (await reindex()).json()).toEqual({ scanned: 1, indexed: 1, skipped: 0 });
    const [artifact] = await listAll();
    expect(artifact).toMatchObject({ id: ID, title: "Older", email: "maker@example.test", session: "s1", revisions: 1 });
  });

  test("leaves an artifact the index already has alone", async () => {
    await publishAs(ID, { title: "Current", email: "maker@example.test" });
    await publishAs(ID, { title: "Current v2", email: "fixer@example.test" });

    expect(await (await reindex()).json()).toEqual({ scanned: 1, indexed: 0, skipped: 1 });
    const [artifact] = await listAll();
    // Still the revision history the publishes built, not a reset to one.
    expect(artifact).toMatchObject({ title: "Current v2", email: "maker@example.test", revisions: 2 });
  });

  test("is idempotent", async () => {
    await publishAs(ID, { title: "Older" });
    index.db.exec("DELETE FROM artifacts");
    await reindex();
    expect(await (await reindex()).json()).toEqual({ scanned: 1, indexed: 0, skipped: 1 });
    expect(await listAll()).toHaveLength(1);
  });

  // One request covers one page, so its cost does not grow with the bucket.
  // The cursor it hands back is how a caller reaches the rest.
  test("covers one page per request and hands back the cursor", async () => {
    await publishAs(ID, { title: "One" });
    await publishAs(OTHER, { title: "Two" });
    await publishAs("Q0NDQ0NDQ0NDQ0NDQ0NDQw", { title: "Three" });
    index.db.exec("DELETE FROM artifacts");
    (env.ARTIFACTS as unknown as FakeBucket).pageSize = 1;

    expect(await (await reindex()).json()).toEqual({ scanned: 1, indexed: 1, skipped: 0, cursor: "1" });
    expect(await listAll()).toHaveLength(1);
  });

  // A store big enough to page is exactly the one that must not be
  // half-indexed: following the cursor to the end reaches every artifact.
  test("following the cursor indexes every page", async () => {
    await publishAs(ID, { title: "One" });
    await publishAs(OTHER, { title: "Two" });
    await publishAs("Q0NDQ0NDQ0NDQ0NDQ0NDQw", { title: "Three" });
    index.db.exec("DELETE FROM artifacts");
    (env.ARTIFACTS as unknown as FakeBucket).pageSize = 1;

    let cursor: string | undefined;
    let indexed = 0;
    let requests = 0;
    do {
      const reply = (await (await reindex(cursor)).json()) as { indexed: number; cursor?: string };
      indexed += reply.indexed;
      cursor = reply.cursor;
      requests += 1;
    } while (cursor);
    expect({ indexed, requests }).toEqual({ indexed: 3, requests: 3 });
    expect(await listAll()).toHaveLength(3);
  });

  test("refuses a body that is not a cursor", async () => {
    const body = "[1, 2]";
    const response = await worker.fetch(
      new Request("https://artifacts.test/reindex", {
        method: "POST",
        body,
        headers: { "X-Artifact-Auth": await signed("POST", "/reindex", body) },
      }),
      env
    );
    expect(response.status).toBe(400);
  });

  // The body is signed, so a cursor cannot ride on a signature made for a
  // different one.
  test("refuses a cursor the signature does not cover", async () => {
    const response = await worker.fetch(
      new Request("https://artifacts.test/reindex", {
        method: "POST",
        body: JSON.stringify({ cursor: "1" }),
        headers: { "X-Artifact-Auth": await signed("POST", "/reindex", "") },
      }),
      env
    );
    expect(response.status).toBe(401);
  });

  test("refuses an unsigned request", async () => {
    const response = await worker.fetch(new Request("https://artifacts.test/reindex", { method: "POST" }), env);
    expect(response.status).toBe(401);
  });
});

test("answers 503 rather than serving anything unsigned when no key is configured", async () => {
  const response = await worker.fetch(new Request("https://artifacts.test/a"), {
    ...env,
    ARTIFACT_SIGNING_KEY: "",
  });
  expect(response.status).toBe(503);
});
