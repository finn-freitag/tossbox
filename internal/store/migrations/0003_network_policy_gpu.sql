-- Per-device outbound network policy and GPU sharing.
ALTER TABLE devices ADD COLUMN network_policy text NOT NULL DEFAULT 'internet';
ALTER TABLE devices ADD COLUMN gpu boolean NOT NULL DEFAULT false;
