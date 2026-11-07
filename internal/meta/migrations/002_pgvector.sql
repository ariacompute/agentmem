-- Best-effort: only applied when the pgvector extension is available.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS concept_vectors (
    concept_id TEXT PRIMARY KEY,
    embedding  vector(1536) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_concept_vectors_embedding
    ON concept_vectors USING hnsw (embedding vector_cosine_ops);
