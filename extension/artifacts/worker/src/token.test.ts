/**
 * The cross-language half of the token contract.
 *
 * These vectors were minted by Go (extension/artifacts/token_test.go,
 * TestVectorsAreStable). If the two implementations ever disagree about
 * encoding, field names or the canonical string, this test fails instead of
 * a deploy silently rejecting every link the fleet has handed out.
 */
import { expect, test, describe } from "bun:test";
import vectors from "../../testdata/vectors.json";
import { mintLink, verifyLink, verifyRequest, AUTH_SKEW_SECONDS } from "./token";

const encoder = new TextEncoder();
const body = encoder.encode(vectors.request.body).buffer as ArrayBuffer;

describe("links minted by Go", () => {
  test("verify here", async () => {
    const claims = await verifyLink(vectors.key, vectors.link.token, vectors.link.id, vectors.link.exp - 3600);
    expect(claims).not.toBeNull();
    expect(claims!.id).toBe(vectors.link.id);
  });

  test("are scoped to one artifact", async () => {
    const claims = await verifyLink(vectors.key, vectors.link.token, "another-artifact", vectors.link.exp - 3600);
    expect(claims).toBeNull();
  });

  test("expire", async () => {
    const claims = await verifyLink(vectors.key, vectors.link.token, vectors.link.id, vectors.link.exp + 1);
    expect(claims).toBeNull();
  });

  test("do not verify under another key", async () => {
    const claims = await verifyLink("another-key", vectors.link.token, vectors.link.id, vectors.link.exp - 3600);
    expect(claims).toBeNull();
  });

  test("survive a key stored with a trailing newline", async () => {
    // The failure this guards against looks like a working gate: it
    // refuses, but it refuses everything.
    const claims = await verifyLink(`${vectors.key}\n`, vectors.link.token, vectors.link.id, vectors.link.exp - 3600);
    expect(claims).not.toBeNull();
  });
});

describe("links minted here", () => {
  // The index page hands out links the Worker minted itself. If they
  // differed from Go's by a byte, a link copied from the page and one a
  // session minted for the same artifact would disagree about validity.
  test("are byte-identical to Go's for the same artifact and expiry", async () => {
    expect(await mintLink(vectors.key, vectors.link.id, vectors.link.exp)).toBe(vectors.link.token);
  });
});

describe("requests signed by Go", () => {
  test("verify here", async () => {
    const ok = await verifyRequest(
      vectors.key,
      vectors.request.header,
      vectors.request.method,
      vectors.request.path,
      body,
      vectors.request.ts
    );
    expect(ok).toBe(true);
  });

  test("are refused outside the clock skew", async () => {
    const ok = await verifyRequest(
      vectors.key,
      vectors.request.header,
      vectors.request.method,
      vectors.request.path,
      body,
      vectors.request.ts + AUTH_SKEW_SECONDS + 1
    );
    expect(ok).toBe(false);
  });

  test("cannot be replayed onto another artifact", async () => {
    const ok = await verifyRequest(
      vectors.key,
      vectors.request.header,
      vectors.request.method,
      "/a/some-other-artifact",
      body,
      vectors.request.ts
    );
    expect(ok).toBe(false);
  });

  test("cannot be replayed with different content", async () => {
    const ok = await verifyRequest(
      vectors.key,
      vectors.request.header,
      vectors.request.method,
      vectors.request.path,
      encoder.encode("something else").buffer as ArrayBuffer,
      vectors.request.ts
    );
    expect(ok).toBe(false);
  });

  test("are refused without a header", async () => {
    const ok = await verifyRequest(vectors.key, null, vectors.request.method, vectors.request.path, body, vectors.request.ts);
    expect(ok).toBe(false);
  });
});
