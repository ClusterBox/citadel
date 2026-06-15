CREATE TABLE IF NOT EXISTS services (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  env TEXT NOT NULL,
  region TEXT NOT NULL,
  runtime TEXT NOT NULL,
  log_group TEXT NOT NULL,
  repo_path TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ingest_cursor (
  service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  last_ts INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS error_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  ts INTEGER NOT NULL,
  status INTEGER,
  level TEXT,
  message TEXT NOT NULL,
  request_id TEXT,
  stack TEXT,
  raw TEXT NOT NULL,
  log_stream TEXT,
  cw_event_id TEXT UNIQUE
);

CREATE INDEX IF NOT EXISTS idx_error_events_service_ts
  ON error_events(service_id, ts DESC);
