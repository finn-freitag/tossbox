// Keep the embed directory non-empty in git (the Go backend embeds ../internal/webui/dist).
import { writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
writeFileSync(fileURLToPath(new URL('../../internal/webui/dist/.gitkeep', import.meta.url)), '')
