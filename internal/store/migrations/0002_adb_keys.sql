-- adb public keys of users (copied to their Android devices at creation).
ALTER TABLE users ADD COLUMN adb_keys jsonb NOT NULL DEFAULT '[]';
ALTER TABLE devices ADD COLUMN adb_keys jsonb NOT NULL DEFAULT '[]';
