# Tossbox

Self-hosted **throwaway devices**. Click "Create device", pick an OS template, RAM, CPU and storage, and get a VM, container, macOS VM or phone with SSH / RDP / VNC / ADB credentials a few moments later. Use it remotely or from CI, then throw it away.

Tossbox is built like a game-server panel:

| Component | Repo | Role |
|---|---|---|
| **Dashboard** | this repo | Web UI, REST API, scheduler, gateway (Docker) |
| **Node agent** | [tb-agent](https://github.com/finn-freitag/tb-agent) | Runs on each host and creates devices via drivers (libvirt, docker-qemu, lxc, android, orchard, usb-phone) |
| **Templates** | [tb-templates](https://github.com/finn-freitag/tb-templates) | OS templates ("eggs") and golden image builds |
| **Protocol** | [tb-proto](https://github.com/finn-freitag/tb-proto) | gRPC contract between dashboard and agents |

## How traffic flows

```
client ──► dashboard-host:40001 ──(your network, e.g. Tailscale)──► node:40001 ──► device:3389
             gateway                                                agent
```

- Every exposed service of a device (SSH, RDP, VNC, ADB, HTTP, HTTPS) gets its own port from `TOSSBOX_PORT_RANGE`. The gateway on the dashboard host listens on it and forwards to the **same port** on the node, where the agent forwards to the device.
- Each device gets a hostname under the base domain, e.g. `machine1.servers.example.com`. For SSH/RDP/VNC the port selects the device. Ports 80/443 of the device are reachable through the name itself: HTTP is routed by Host header, HTTPS by SNI (TLS passthrough).
- The dashboard has to reach every node's address (e.g. its Tailscale IP). Setting that up is your job; Tossbox treats the address as reachable.

## Quick start

1. **DNS:** point a wildcard record at the dashboard host:
   ```
   *.servers.example.com.   A   203.0.113.10
   dashboard.example.com.   A   203.0.113.10
   ```
2. **Configure:**
   ```bash
   curl -LO https://raw.githubusercontent.com/finn-freitag/tossbox/main/docker-compose.yml
   curl -L -o .env https://raw.githubusercontent.com/finn-freitag/tossbox/main/.env.example
   ```
   Edit `.env`: at least `TOSSBOX_BASE_DOMAIN`, `POSTGRES_PASSWORD`, `TOSSBOX_ADMIN_PASSWORD` and `TOSSBOX_SECRET_KEY` (`openssl rand -hex 32`).
3. **Start:**
   ```bash
   docker compose up -d
   ```
   The UI runs on `http://<host>:8080`, and on `https://dashboard.example.com` if `TOSSBOX_DASHBOARD_HOST` and `TOSSBOX_ACME_EMAIL` are set.
4. **Open the firewall** for TCP 80, 443, 7443 (agents) and the port range (default 40000–49999).

The container uses `network_mode: host`, because the gateway opens and closes ports at runtime.

### Already running a reverse proxy on 80/443?

Either set `TOSSBOX_GATEWAY_HTTP_LISTEN` / `TOSSBOX_GATEWAY_HTTPS_LISTEN` to other ports and let your proxy forward `*.servers.example.com` there (HTTPS as TCP/SNI passthrough), or move your proxy to other ports and set `TOSSBOX_GATEWAY_FALLBACK_HTTP` / `TOSSBOX_GATEWAY_FALLBACK_HTTPS` so Tossbox forwards all unknown hosts to it.

## Adding a node

1. In the dashboard, open **Nodes → Add node**. Enter a name and the address the dashboard host uses to reach the node (e.g. `100.64.0.12`).
2. Copy the enrollment token (`tb1.…`, valid 7 days, single use) and the agent endpoint (`dashboard.example.com:7443`).
3. Install [tb-agent](https://github.com/finn-freitag/tb-agent) on the node and put both into its `node.yaml`. On first start the agent pins the dashboard CA from the token, gets a client certificate and connects over mTLS.

Regenerating a node's token revokes its certificate; the agent has to enroll again.

The agent endpoint name must be in the dashboard's gRPC certificate: `TOSSBOX_DASHBOARD_HOST` is added automatically, other names/IPs go into `TOSSBOX_GRPC_HOSTS`.

## Optional: Guacamole

Browser access to RDP/VNC/SSH via Apache Guacamole, loosely coupled through its JSON auth extension:

```bash
# .env
TOSSBOX_GUACAMOLE_URL=https://guacamole.servers.example.com/guacamole
TOSSBOX_GUACAMOLE_JSON_SECRET=$(openssl rand -hex 16)
```
```bash
docker compose --profile guacamole up -d
```

The gateway serves `guacamole.<base domain>` (TLS via ACME when enabled), and each device gets an **Open in Guacamole** button. The dashboard signs a short-lived login that opens the device's connection directly and also carries the user's other devices, so several Guacamole tabs for different devices work side by side. Guacamole needs no user database. guacd connects to the node address directly.

When the gateway serves the Guacamole host, logins go through a small page on that host (`/.tossbox/guacamole-login`) that starts a fresh Guacamole session for each login. This is on automatically with ACME. If another reverse proxy terminates TLS and forwards the Guacamole host to the gateway, set `TOSSBOX_GUACAMOLE_LOGIN_HELPER=true`. Leave it off if your proxy sends the Guacamole host straight to the Guacamole container.

## Configuration reference

All configuration uses environment variables (see [.env.example](.env.example)).

| Variable | Default | Description |
|---|---|---|
| `TOSSBOX_DATABASE_URL` | `postgres://tossbox:tossbox@127.0.0.1:5432/tossbox?sslmode=disable` | PostgreSQL connection string |
| `TOSSBOX_DATA_DIR` | `/data` | CA, generated secret key, ACME cache |
| `TOSSBOX_SECRET_KEY` | generated | 32 bytes (hex/base64) for AES-GCM encryption of device passwords and license keys |
| `TOSSBOX_BASE_DOMAIN` | `servers.localhost` | Wildcard base domain for device hostnames |
| `TOSSBOX_DASHBOARD_HOST` | | Dashboard hostname served by the gateway on 80/443 |
| `TOSSBOX_PUBLIC_URL` | | Public dashboard URL (`https://…` enables secure cookies) |
| `TOSSBOX_SECURE_COOKIES` | `auto` | `true`/`false`/`auto` (auto = public URL is https) |
| `TOSSBOX_SESSION_TTL` | `720h` | Login session lifetime |
| `TOSSBOX_HTTP_LISTEN` | `:8080` | UI/API listener |
| `TOSSBOX_GRPC_LISTEN` | `:7443` | Agent listener |
| `TOSSBOX_GRPC_HOSTS` | | Extra names/IPs for the gRPC TLS certificate |
| `TOSSBOX_GRPC_ENDPOINT` | `<dashboard host>:7443` | Endpoint shown to admins when adding nodes |
| `TOSSBOX_PORT_RANGE` | `40000-49999` | Device service ports (gateway and nodes) |
| `TOSSBOX_GATEWAY_HTTP_LISTEN` | `:80` | Host-based HTTP routing (empty = off) |
| `TOSSBOX_GATEWAY_HTTPS_LISTEN` | `:443` | SNI-based HTTPS routing (empty = off) |
| `TOSSBOX_GATEWAY_PUBLIC_ADDRESS` | `0.0.0.0` | Bind address for exposure `public` |
| `TOSSBOX_GATEWAY_TAILSCALE_ADDRESS` | | Bind address for exposure `tailscale` (empty = disabled) |
| `TOSSBOX_GATEWAY_FALLBACK_HTTP` | | `host:port` for unknown hosts on :80 |
| `TOSSBOX_GATEWAY_FALLBACK_HTTPS` | | `host:port` for unknown SNI on :443 (passthrough) |
| `TOSSBOX_GATEWAY_RATE_LIMIT` | `60` | New connections per source IP per minute on device ports |
| `TOSSBOX_ACME_EMAIL` | | Enables Let's Encrypt for the dashboard and Guacamole hosts |
| `TOSSBOX_ACME_DIRECTORY_URL` | Let's Encrypt | e.g. the staging directory |
| `TOSSBOX_ACME_CACHE_DIR` | `<data dir>/acme` | Certificate cache |
| `TOSSBOX_TEMPLATE_SOURCES` | tb-templates catalog | Comma separated `catalog.json` URLs or YAML directories |
| `TOSSBOX_ADMIN_USERNAME` / `_PASSWORD` | `admin` / – | Initial admin (only when no users exist) |
| `TOSSBOX_GUACAMOLE_URL` | | Public Guacamole URL (enables the integration) |
| `TOSSBOX_GUACAMOLE_JSON_SECRET` | | 32 hex chars, shared with Guacamole's `JSON_SECRET_KEY` |
| `TOSSBOX_GUACAMOLE_UPSTREAM` | `http://127.0.0.1:8081` | Where the gateway reaches the Guacamole container |
| `TOSSBOX_GUACAMOLE_LOGIN_HELPER` | `auto` | `true` when the gateway serves the Guacamole host behind another proxy (`auto` = on with ACME) |
| `TOSSBOX_TRUSTED_PROXIES` | | Comma separated IPs/CIDRs of reverse proxies in front of Tossbox (e.g. `127.0.0.1,172.16.0.0/12`). Their `X-Forwarded-For` is used for login rate limiting, audit IPs and HTTP allowlists |
| `TOSSBOX_LOG_LEVEL` / `_FORMAT` | `info` / `text` | `debug`…`error` / `text`, `json` |

Runtime settings such as overcommit, default services, password length, default TTL and public VNC are edited by admins in the UI (**Settings**) and stored in the database.

## Security notes

- Passwords are generated per device (≥ 20 characters, letters, digits and `-_.`), shown only to the owner and admins, and stored encrypted.
- Behind a reverse proxy, set `TOSSBOX_TRUSTED_PROXIES`. Otherwise every request seems to come from the proxy: failed logins from anyone then lock out everyone for 10 minutes, and HTTP allowlists see the proxy's address.
- adb on redroid gives a root shell in a privileged container. Android devices therefore require adb authentication: users add their `~/.android/adbkey.pub` under **Profile → ADB keys**, and only those keys can connect to devices they create afterwards. Public ADB exposure is still off by default (**Settings → Allow public ADB**); prefer Tailscale exposure or an allowlist.
- Devices can be created with the `offline` network policy (no outbound traffic at all, still reachable through their services), and can share the node's GPU. See the tb-agent README.
- Node agents only accept device port connections from the dashboard and private networks by default (`allowed_sources` in node.yaml), so the gateway's allowlists and rate limits can't be bypassed from the internet.
- VNC is off by default. Classic VNC auth only uses 8 password characters and isn't encrypted, so prefer Guacamole, an SSH tunnel or Tailscale exposure.
- The gateway rate-limits new connections per source IP on device ports. Devices can have an IP allowlist.
- API tokens (`tbt_…`) and sessions are stored as hashes. Cookie-authenticated writes require the `X-Tossbox-CSRF: 1` header.

## API

See [docs/API.md](docs/API.md). Automation uses `Authorization: Bearer tbt_…`. Devices created with an API token need a `ttl_hours` (configurable).

## Development

```bash
nix-shell                     # go, node, postgres
cd web && npm ci && npm run build && cd ..   # builds the UI into internal/webui/dist
go run ./cmd/tossbox
go test ./...
```

The integration tests need PostgreSQL: set `TOSSBOX_TEST_DATABASE_URL=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable` (the tests create and drop their own databases).

Layout:

```
cmd/tossbox          main
internal/api         REST handlers, auth, SSE
internal/core        application service (devices, nodes, users, templates, gRPC hooks)
internal/scheduler   quota, capacity and node selection (pure)
internal/store       PostgreSQL + migrations
internal/grpcserver  agent enrollment and control stream
internal/gateway     TCP / HTTP / HTTPS forwarding, ACME
internal/catalog     template loader
internal/pki         dashboard CA
internal/guac        Guacamole JSON auth
internal/secrets     encryption, passwords, tokens
internal/webui       embedded UI (built from web/)
web/                 React UI
```

## License

MIT
