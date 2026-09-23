/**
 * An in-memory D1 for tests, backed by bun:sqlite and built from the real
 * migration file, so the SQL the Worker runs is checked against the schema
 * that actually ships rather than a hand-kept copy of it.
 *
 * This is test setup against a throwaway in-memory database. It is not a
 * migration of any environment; the real one is applied by a human.
 */
import { Database } from "bun:sqlite";
import { readFileSync } from "node:fs";
import { join } from "node:path";

class Statement {
  private values: unknown[] = [];
  constructor(
    private readonly db: Database,
    private readonly sql: string,
    private readonly fail: () => boolean
  ) {}

  bind(...values: unknown[]): Statement {
    this.values = values;
    return this;
  }

  async run() {
    if (this.fail()) throw new Error("D1 unavailable");
    this.db.query(this.sql).run(...(this.values as never[]));
    return { results: [], success: true };
  }

  async all<T>() {
    if (this.fail()) throw new Error("D1 unavailable");
    return { results: this.db.query(this.sql).all(...(this.values as never[])) as T[], success: true };
  }
}

export class FakeD1 {
  readonly db = new Database(":memory:");
  failing = false;

  constructor() {
    this.db.exec(readFileSync(join(import.meta.dir, "..", "migrations", "0001_artifacts.sql"), "utf8"));
  }

  prepare(sql: string): Statement {
    return new Statement(this.db, sql, () => this.failing);
  }
}
