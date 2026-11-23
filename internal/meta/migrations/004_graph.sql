CREATE TABLE IF NOT EXISTS graph_nodes (
    concept_id   TEXT PRIMARY KEY,
    node_type    TEXT NOT NULL DEFAULT '',
    label        TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    visibility   TEXT NOT NULL DEFAULT 'private',
    owner        TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'stable',
    trust        TEXT NOT NULL DEFAULT 'unverified',
    properties   JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_count INTEGER NOT NULL DEFAULT 0,
    has_resource BOOLEAN NOT NULL DEFAULT FALSE,
    content_hash TEXT NOT NULL DEFAULT '',
    commit_sha   TEXT NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_type ON graph_nodes (node_type);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_hash ON graph_nodes (content_hash);

CREATE TABLE IF NOT EXISTS graph_edges (
    from_id      TEXT NOT NULL,
    to_id        TEXT NOT NULL,
    edge_type    TEXT NOT NULL,
    origin       TEXT NOT NULL DEFAULT 'declared',
    evidence     TEXT NOT NULL DEFAULT '',
    properties   JSONB NOT NULL DEFAULT '{}'::jsonb,
    content_hash TEXT NOT NULL DEFAULT '',
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (from_id, to_id, edge_type)
);
CREATE INDEX IF NOT EXISTS idx_graph_edges_to ON graph_edges (to_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_type ON graph_edges (edge_type);
