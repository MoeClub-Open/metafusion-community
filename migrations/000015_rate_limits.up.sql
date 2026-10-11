-- Runtime policy and counters belong to community; no cross-service database access.
CREATE TABLE IF NOT EXISTS community.rate_limit_policy (
  singleton boolean PRIMARY KEY CHECK (singleton),
  etag text NOT NULL,
  policy jsonb NOT NULL,
  messages jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid
);
INSERT INTO community.rate_limit_policy(singleton,etag,policy,messages)
VALUES (true,'community-rate-limits-v1',
  '{"default_per_minute":180,"groups":{"admin":{"unlimited":true}}}'::jsonb,
  '{"send_per_minute":180,"stranger_per_hour":5,"admin_unlimited":true}'::jsonb)
ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE IF NOT EXISTS community.rate_limit_windows (
  subject_key text PRIMARY KEY,
  window_start timestamptz NOT NULL,
  used integer NOT NULL CHECK (used > 0),
  expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS rate_limit_windows_expiry_idx ON community.rate_limit_windows(expires_at);

CREATE TABLE IF NOT EXISTS community.message_rate_limit_buckets (
  subject_key text PRIMARY KEY,
  tokens double precision NOT NULL CHECK (tokens >= 0),
  capacity integer NOT NULL CHECK (capacity > 0),
  window_seconds integer NOT NULL CHECK (window_seconds > 0),
  updated_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  allowed boolean NOT NULL,
  retry_after integer NOT NULL
);
CREATE INDEX IF NOT EXISTS message_rate_limit_buckets_expiry_idx ON community.message_rate_limit_buckets(expires_at);
