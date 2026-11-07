CREATE TABLE IF NOT EXISTS computation_runs (
    id              BIGSERIAL PRIMARY KEY,
    concept_id      TEXT NOT NULL DEFAULT '',
    subject         TEXT NOT NULL DEFAULT '',
    runtime         TEXT NOT NULL DEFAULT '',
    contract_hash   TEXT NOT NULL DEFAULT '',
    params_digest   TEXT NOT NULL DEFAULT '',
    job_id          TEXT NOT NULL DEFAULT '',
    verdict         TEXT NOT NULL DEFAULT '',
    display_allowed BOOLEAN NOT NULL DEFAULT FALSE,
    provenance      BOOLEAN NOT NULL DEFAULT FALSE,
    fidelity        BOOLEAN NOT NULL DEFAULT FALSE,
    stale           BOOLEAN NOT NULL DEFAULT FALSE,
    reasons         JSONB NOT NULL DEFAULT '[]'::jsonb,
    error_class     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (job_id, verdict)
);
CREATE INDEX IF NOT EXISTS idx_computation_runs_concept ON computation_runs (concept_id);
CREATE INDEX IF NOT EXISTS idx_computation_runs_created ON computation_runs (created_at);
CREATE INDEX IF NOT EXISTS idx_computation_runs_contract ON computation_runs (contract_hash);
