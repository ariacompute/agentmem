CREATE TABLE IF NOT EXISTS concepts_meta (
    concept_id    TEXT PRIMARY KEY,
    content_hash  TEXT NOT NULL,
    commit_sha    TEXT NOT NULL DEFAULT '',
    visibility    TEXT NOT NULL DEFAULT 'private',
    owner         TEXT NOT NULL DEFAULT 'system',
    status        TEXT NOT NULL DEFAULT 'stable',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_concepts_meta_hash ON concepts_meta (content_hash);
CREATE INDEX IF NOT EXISTS idx_concepts_meta_created ON concepts_meta (created_at);

CREATE TABLE IF NOT EXISTS projections (
    id            BIGSERIAL PRIMARY KEY,
    concept_id    TEXT NOT NULL,
    adapter       TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    fingerprint   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (concept_id, adapter)
);
CREATE INDEX IF NOT EXISTS idx_projections_status ON projections (status);
CREATE INDEX IF NOT EXISTS idx_projections_hash ON projections (content_hash);

CREATE TABLE IF NOT EXISTS decisions (
    id           BIGSERIAL PRIMARY KEY,
    concept_id   TEXT NOT NULL DEFAULT '',
    rule_id      TEXT NOT NULL DEFAULT '',
    reason       TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT 'rule',
    confidence   DOUBLE PRECISION NOT NULL DEFAULT 0,
    targets      JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_decisions_concept ON decisions (concept_id);
CREATE INDEX IF NOT EXISTS idx_decisions_created ON decisions (created_at);

CREATE TABLE IF NOT EXISTS migration_cursors (
    job             TEXT PRIMARY KEY,
    last_concept_id TEXT NOT NULL DEFAULT '',
    done            BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS acl (
    subject    TEXT NOT NULL,
    concept_id TEXT NOT NULL,
    allow      BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (subject, concept_id)
);

CREATE TABLE IF NOT EXISTS engine_fingerprints (
    adapter     TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
