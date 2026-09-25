-- The artifact index: one row per artifact, so the team can see who made
-- what without listing the bucket. R2 keeps the bodies; this keeps who,
-- when and how often.
--
-- created_* is written once, by the first publish, and never again. updated_*
-- moves with every revision. Keeping them apart is the point: "who made this"
-- and "who last touched it" are different questions, and a revision by a
-- teammate must not rewrite the answer to the first one.
CREATE TABLE artifacts (
  id              TEXT PRIMARY KEY,
  title           TEXT    NOT NULL,
  content_type    TEXT    NOT NULL,
  bytes           INTEGER NOT NULL,
  created_by      TEXT    NOT NULL DEFAULT '',
  created_session TEXT    NOT NULL DEFAULT '',
  created_at      TEXT    NOT NULL,
  updated_by      TEXT    NOT NULL DEFAULT '',
  updated_session TEXT    NOT NULL DEFAULT '',
  updated_at      TEXT    NOT NULL,
  revisions       INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX artifacts_by_updated ON artifacts (updated_at DESC);
CREATE INDEX artifacts_by_creator ON artifacts (created_by COLLATE NOCASE);
