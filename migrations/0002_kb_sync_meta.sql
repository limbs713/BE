CREATE TABLE IF NOT EXISTS kb_sync_meta (
  id             int PRIMARY KEY DEFAULT 1,
  last_synced_at timestamptz,
  CONSTRAINT kb_sync_meta_singleton CHECK (id = 1)
);
INSERT INTO kb_sync_meta (id, last_synced_at) VALUES (1, NULL) ON CONFLICT (id) DO NOTHING;
