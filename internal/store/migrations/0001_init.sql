CREATE TABLE users (
    id            uuid PRIMARY KEY,
    username      text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    role          text NOT NULL CHECK (role IN ('admin', 'user')),
    ssh_keys      jsonb NOT NULL DEFAULT '[]',
    -- Quota limits (model.Quota). Kept as JSON: they are always read and
    -- written as a whole and only interpreted by the application.
    quota         jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz
);

CREATE TABLE nodes (
    id                   uuid PRIMARY KEY,
    name                 text NOT NULL UNIQUE,
    address              text NOT NULL,
    enrolled             boolean NOT NULL DEFAULT false,
    cert_serial          text NOT NULL DEFAULT '',
    token_hash           text,
    token_expires_at     timestamptz,
    agent_version        text NOT NULL DEFAULT '',
    last_seen            timestamptz,
    capabilities         jsonb NOT NULL DEFAULT '{}',
    usage                jsonb NOT NULL DEFAULT '{}',
    created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX nodes_token_hash_idx ON nodes (token_hash) WHERE token_hash IS NOT NULL;

CREATE TABLE devices (
    id               uuid PRIMARY KEY,
    owner_id         uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    node_id          uuid REFERENCES nodes(id) ON DELETE RESTRICT,
    driver           text NOT NULL,
    template_id      text NOT NULL,
    template_name    text NOT NULL,
    template_family  text NOT NULL,
    -- Snapshot of the resolved TemplateSpec sent to the agent, so catalog
    -- changes never affect existing devices.
    template_spec    jsonb NOT NULL,
    name             text NOT NULL,
    hostname         text NOT NULL UNIQUE,
    vcpus            integer NOT NULL,
    cpu_cap_percent  integer NOT NULL DEFAULT 0,
    ram_mb           bigint NOT NULL,
    storage_gb       bigint NOT NULL,
    power            text NOT NULL CHECK (power IN ('running', 'stopped')),
    state            text NOT NULL DEFAULT 'pending',
    ready            boolean NOT NULL DEFAULT false,
    status_message   text NOT NULL DEFAULT '',
    internal_ip      text NOT NULL DEFAULT '',
    storage_used_mb  bigint NOT NULL DEFAULT 0,
    exposure         text NOT NULL CHECK (exposure IN ('public', 'tailscale', 'none')),
    allowlist        jsonb NOT NULL DEFAULT '[]',
    license_mode     text NOT NULL DEFAULT 'none',
    license_key_enc  text NOT NULL DEFAULT '',
    username         text NOT NULL,
    password_enc     text NOT NULL,
    ssh_keys         jsonb NOT NULL DEFAULT '[]',
    params           jsonb NOT NULL DEFAULT '{}',
    ttl_hours        integer NOT NULL DEFAULT 0,
    expires_at       timestamptz,
    revision         bigint NOT NULL DEFAULT 1,
    deleting         boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX devices_owner_idx ON devices (owner_id);
CREATE INDEX devices_node_idx ON devices (node_id);
CREATE INDEX devices_expires_idx ON devices (expires_at) WHERE expires_at IS NOT NULL;

-- One row per exposed service. public_port is the port allocation: the same
-- port number is used on the gateway and on the node.
CREATE TABLE device_services (
    device_id     uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name          text NOT NULL,
    internal_port integer NOT NULL,
    public_port   integer NOT NULL UNIQUE,
    PRIMARY KEY (device_id, name)
);

-- Windows license pool leases (the keys themselves stay on the node).
CREATE TABLE license_leases (
    device_id  uuid PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    node_id    uuid NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    pool       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE settings (
    id    integer PRIMARY KEY CHECK (id = 1),
    value jsonb NOT NULL
);

CREATE TABLE audit_log (
    id      bigserial PRIMARY KEY,
    at      timestamptz NOT NULL DEFAULT now(),
    user_id uuid,
    action  text NOT NULL,
    target  text NOT NULL DEFAULT '',
    detail  jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_at_idx ON audit_log (at);
