# The artifact store

A Cloudflare Worker holding documents agents publish, served to whoever has
the link.

It exists because the artifacts built into a coding CLI belong to the
account that published them. When sessions run on more than one account, or
on more than one CLI, an artifact published by one session is unreadable
from the next — the opposite of what an artifact is for. This store belongs
to whoever deploys it instead, so every session configured with its URL and
key reaches the same artifacts whatever account it is on and whatever CLI it
runs.

## The shape

```
PUT /a/<id>    signed         publish or replace an artifact
GET /a/<id>?k= link key       read one artifact
GET /a         signed         list what the team has published, and who made it
GET /?k=       index key      a page listing every artifact, who made it, and a link to each
POST /reindex  signed         index one page of bucket artifacts the index is missing
```

## Who made what

Every publish is recorded in a D1 table, `artifacts` (`migrations/0001_artifacts.sql`), beside
the body in R2. It keeps two answers apart on purpose:

- `created_by` / `created_at` — the first publish of an id, written once and never again;
- `updated_by` / `updated_at` / `revisions` — every later one.

So a teammate fixing a typo in someone's report never becomes its author, and `list_artifacts
by=<email>` answers "what did they make", not "what did they touch". The row is written after
the body is stored: a body with no row is only unlisted, and publishing the same id again
repairs it, whereas a row pointing at no body would be a listing that 404s. The timestamp is the
Worker's clock, not the publisher's.

The names are what each publisher's `identity_command` reported — attribution for a trusted
team, not an audit trail, since everyone publishes with one key.

To query it directly: Cloudflare dashboard → Workers & Pages → D1 → your index database →
Console, e.g. `SELECT title, created_by, created_at, revisions FROM artifacts ORDER BY
updated_at DESC`.

## Backfilling the index

An artifact published before the index existed, or one whose index write
failed, is in the bucket and in no row. It still opens by link, but it lists
nowhere. `scripts/reindex.sh` walks the bucket and inserts the missing rows:

```bash
printf '%s' "$key" | ARTIFACT_BASE_URL=https://artifacts.example.com ./scripts/reindex.sh
```

Each `POST /reindex` covers one page of the bucket (100 keys) and answers with
a `cursor` until the bucket is exhausted, so one request costs the same however
large the store is. The script signs each page's request with the key on
stdin, the same `ARTIFACT_SIGNING_KEY` the Worker holds, follows the cursor to
the end, and prints how many objects it scanned, indexed and skipped. Any
answer other than 2xx stops it with a non-zero exit.

It is idempotent, so rerunning it after a deploy costs nothing when nothing is
missing. A backfilled artifact reads as one version by whoever published it,
since the upload time, size and the publisher metadata are all the bucket
knows after the fact.

## The index page

`list_artifacts` with `index_link: true` returns a link to a page listing every artifact, with
who made it and a working link to each. **That link opens everything on the page**, so it is
minted for 24 hours rather than 30 days, and the links on the page expire with it. An index key
opens no artifact and an artifact key does not open the index. The page runs no script: its
content security policy allows none, so a title that got past escaping still could not execute.

`<id>` is 128 bits of randomness minted by the client, so an id is not
guessable even before its key is checked. That is defence in depth; the
signature on the link is the control.

## One secret, two jobs

`ARTIFACT_SIGNING_KEY` verifies publish and list requests, and every read
link is a signature minted with it. So:

- holding a link never confers the ability to publish — a link is an output
  of the key, not the key;
- everyone who can publish can mint a link for anything they published;
- there is no second credential to distribute, rotate or leak.

Rotating the key invalidates every link already handed out. That is the
revocation mechanism, and it is all-or-nothing: there is no per-artifact
revocation short of deleting the object.

## What a link is, and is not

A link is a **bearer credential** scoped to one artifact, with an expiry.
Whoever holds it is in. The design leans on the two things that actually
bound the damage — a short life and a per-artifact scope, so a link to one
artifact cannot open another.

It is **not identity**. A link says someone was given it, never who is
using it. When it matters who looked, that is Cloudflare Access, and the two
are deliberately not interchangeable.

Two consequences worth stating plainly:

- **An artifact's own JavaScript can read its own key** out of the URL and
  send it anywhere. That is inherent to any key-in-URL scheme, and it is why
  nothing whose disclosure would matter belongs in an artifact.
- **A wrong key and a missing artifact answer identically** (404, same
  body). Otherwise the read endpoint reports which ids exist to anyone
  willing to ask.

## Running the tests

```bash
bun test                                     # routes and the token contract
tsc --project tsconfig.json                  # types; needs a tsc on PATH
```

`src/token.test.ts` verifies vectors minted by Go in
`../testdata/vectors.json`. Two implementations of one wire format is a
liability, so a change to either side that breaks the other fails a test
rather than a deploy: regenerate with

```bash
UPDATE_VECTORS=1 go test ./extension/artifacts/ -run TestVectorsAreStable
```

and expect `TestVectorsAreStable` to fail loudly if the format moved, because
at that point every link already issued is about to stop verifying.

## Deploying your own

The store runs on your own Cloudflare account. R2 must be enabled on the
account before a bucket can be created, and wrangler needs Node 22 or newer. `wrangler.jsonc` names the Worker, the R2
bucket and the D1 database `gate-inbox-artifacts`; change those names there
first if you want different ones, and uncomment `routes` with a hostname you
control to serve from your own domain instead of `workers.dev`. Serve
artifacts from a hostname of their own, so their origin shares nothing with
anything else you run.

From this directory, with `CLOUDFLARE_API_TOKEN` set to a token scoped to the
account you mean to deploy into:

```bash
npx wrangler r2 bucket create gate-inbox-artifacts
npx wrangler d1 create gate-inbox-artifacts
npx wrangler d1 migrations apply gate-inbox-artifacts --remote
key="$(openssl rand -base64 48 | tr -d '\n')"   # save it in your secret store now
printf '%s' "$key" | npx wrangler secret put ARTIFACT_SIGNING_KEY --name gate-inbox-artifacts
npx wrangler deploy
```

Without an explicit token wrangler falls back to whatever OAuth login is on
the machine, which may be a different account from the one you meant.

The D1 database is bound by name, with no `database_id`, so
`wrangler.jsonc` needs no edit once it exists. Review a migration before
applying it, and apply it before deploying code that needs it: otherwise
every publish fails against a database with no table in it.

Secret first, then code — `wrangler secret put` creates the Worker as a
code-less stub if it does not exist, so the key genuinely can land before
any code does, and it has to: a Worker without the key answers 503 to
everything, and one with the *wrong* key silently rejects every link
already handed out.

`tr -d '\n'` is not cosmetic. A key stored with a trailing newline signs
differently from the same key without one, so the store would reject
everything while looking like it works. Both verifiers trim, so this is
belt and braces, but it is an easy failure to cause and a hard one to spot.

Save the key before setting it: a Worker secret cannot be read back. On the
board, point `[extensions.artifacts]` `base_url` at the Worker's URL and make
`key_command` print the same key.
