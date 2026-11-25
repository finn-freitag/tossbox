-- Measured device usage reported by the agent (RAM, CPU share of the node).
ALTER TABLE devices ADD COLUMN memory_used_mb bigint NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN cpu_used_percent double precision NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN usage_at timestamptz;
