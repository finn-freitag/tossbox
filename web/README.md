# Tossbox web UI

React + TypeScript + Vite single-page app for the Tossbox dashboard. The production build is written to `../internal/webui/dist` and embedded into the Go binary.

The REST contract is [`../docs/API.md`](../docs/API.md); the typed client lives in `src/api/` (`types.ts` mirrors the API objects).

## Development

```bash
npm install
npm run dev        # http://localhost:5173, proxies /api (incl. SSE /api/events) to http://localhost:8080
npm run dev:mock   # no backend needed: in-memory fake API with fixtures (VITE_MOCK=1)
```

Start the Go backend on `:8080` for `npm run dev`. In mock mode any login works, devices "provision" in a few seconds, and state resets on a full page reload.

## Checks and build

```bash
npm run typecheck  # tsc --noEmit
npm run build      # typecheck + vite build into ../internal/webui/dist (keeps a .gitkeep there)
```

## Structure

- `src/api/`: `client.ts` (fetch wrapper: CSRF header, 401 handling), `hooks.ts` (TanStack Query hooks + cache helpers), `events.ts` (live updates via `/api/events`), `mock.ts` (fake backend)
- `src/components/`: layout, shared UI widgets (`ui.tsx`), device parts (services, power switch, actions)
- `src/pages/`: Devices, Create device, Device detail, Profile, Login and `admin/` (Nodes, Users, Templates, Settings)
- `src/styles.css`: hand-written CSS with design tokens; light/dark via `prefers-color-scheme` and a manual toggle (`data-theme` on `<html>`)
