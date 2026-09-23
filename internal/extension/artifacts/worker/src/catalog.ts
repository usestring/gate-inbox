/**
 * The artifact index in D1: who published what, when, and how often it was
 * revised. R2 keeps the bodies; this keeps the answers to "whose is this".
 *
 * Named catalog rather than index so it cannot be confused with index.ts,
 * the Worker's entry point.
 */

export interface Entry {
  id: string;
  title: string;
  content_type: string;
  bytes: number;
  created_by: string;
  created_session: string;
  created_at: string;
  updated_by: string;
  updated_session: string;
  updated_at: string;
  revisions: number;
}

export interface Publish {
  id: string;
  title: string;
  contentType: string;
  bytes: number;
  email: string;
  session: string;
  at: string;
}

/**
 * Record one publish. The first publish of an id writes created_* and
 * updated_* alike; every later one moves only updated_* and the revision
 * count, so a teammate revising an artifact never rewrites who made it.
 */
export async function record(db: D1Database, publish: Publish): Promise<void> {
  await db
    .prepare(
      `INSERT INTO artifacts
         (id, title, content_type, bytes, created_by, created_session, created_at,
          updated_by, updated_session, updated_at, revisions)
       VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?5, ?6, ?7, 1)
       ON CONFLICT (id) DO UPDATE SET
         title           = excluded.title,
         content_type    = excluded.content_type,
         bytes           = excluded.bytes,
         updated_by      = excluded.updated_by,
         updated_session = excluded.updated_session,
         updated_at      = excluded.updated_at,
         revisions       = artifacts.revisions + 1`
    )
    .bind(publish.id, publish.title, publish.contentType, publish.bytes, publish.email, publish.session, publish.at)
    .run();
}

// D1 caps the parameters one statement may bind at 100.
const MAX_BOUND = 100;

/**
 * Which of ids the index already has, so a backfill can skip them. Asks
 * about those ids alone: loading every indexed id grows with the index, and
 * a backfill request has to cost the same however big the store has become.
 */
export async function indexedAmong(db: D1Database, ids: string[]): Promise<Set<string>> {
  const known = new Set<string>();
  for (let start = 0; start < ids.length; start += MAX_BOUND) {
    const chunk = ids.slice(start, start + MAX_BOUND);
    const { results } = await db
      .prepare(`SELECT id FROM artifacts WHERE id IN (${chunk.map(() => "?").join(", ")})`)
      .bind(...chunk)
      .all<{ id: string }>();
    for (const row of results) known.add(row.id);
  }
  return known;
}

/**
 * Insert one artifact that the index never saw -- published before the index
 * existed, or stored when its write failed. Unlike record it is not a
 * revision: created_* and updated_* both come from what the bucket knows, and
 * the revision count starts at one, because a backfill is discovering an
 * artifact rather than watching it change.
 */
export async function backfill(db: D1Database, publish: Publish): Promise<void> {
  await db
    .prepare(
      `INSERT INTO artifacts
         (id, title, content_type, bytes, created_by, created_session, created_at,
          updated_by, updated_session, updated_at, revisions)
       VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?5, ?6, ?7, 1)
       ON CONFLICT (id) DO NOTHING`
    )
    .bind(publish.id, publish.title, publish.contentType, publish.bytes, publish.email, publish.session, publish.at)
    .run();
}

/**
 * The most recently changed artifacts, optionally only those one person
 * created. "By" means created: the question the filter answers is "what did
 * they make", and someone who fixed a typo in a teammate's report did not
 * make it.
 */
export async function recent(db: D1Database, limit: number, createdBy: string): Promise<Entry[]> {
  const statement = createdBy
    ? db
        .prepare("SELECT * FROM artifacts WHERE created_by = ?1 COLLATE NOCASE ORDER BY updated_at DESC LIMIT ?2")
        .bind(createdBy, limit)
    : db.prepare("SELECT * FROM artifacts ORDER BY updated_at DESC LIMIT ?1").bind(limit);
  const { results } = await statement.all<Entry>();
  return results;
}
