CREATE TABLE IF NOT EXISTS deployments (
    id           TEXT PRIMARY KEY,
    project      TEXT NOT NULL,
    env          TEXT NOT NULL,
    runtime      TEXT NOT NULL,
    region       TEXT NOT NULL,
    git_sha      TEXT NOT NULL,
    image_uri    TEXT NOT NULL,
    message      TEXT NOT NULL,
    status       TEXT NOT NULL,            -- in_progress | success | failed
    error        TEXT,
    deployed_by  TEXT NOT NULL,
    target       TEXT NOT NULL,            -- resolved service or function name
    started_at   INTEGER NOT NULL,         -- epoch ms
    finished_at  INTEGER,
    duration_ms  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_deployments_started_at ON deployments (started_at DESC);
CREATE INDEX IF NOT EXISTS idx_deployments_project_env ON deployments (project, env);
