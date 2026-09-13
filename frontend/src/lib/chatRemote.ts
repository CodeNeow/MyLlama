// Pure helpers for the chat page's LAN remote tier (phone → PC llama-server):
// profile validation for the Settings form and the endpoint derivation the
// chat transport (lib/chat.ts) consumes. No fetch, no Wails import — kept
// unit-test friendly like the rest of lib/.

import type { ChatEndpoint } from './chat'

/**
 * The persisted remote-chat pairing, mirroring the backend RemoteChatConfig
 * JSON (enabled/host/port/apiKey) as returned by GetConfig and stored in the
 * global appConfig.
 */
export interface RemoteChatProfile {
  enabled: boolean
  host: string
  port: number
  apiKey: string
}

/**
 * Validate a remote host entered in the Settings pairing form: null when the
 * value is usable, otherwise the i18n error key to render inline. An empty
 * (after trimming) host → errHostEmpty; a scheme ("://") or path ("/") →
 * errHostScheme — the pairing stores the bare host part of host[:port] only
 * (the backend SaveRemoteChat enforces the same shape, so a value rejected
 * here would be rejected there too; validating inline gives instant feedback).
 */
export function validateRemoteHost(host: string): string | null {
  const h = host.trim()
  if (!h) return 'settings.remoteChat.errHostEmpty'
  if (h.includes('://') || h.includes('/')) return 'settings.remoteChat.errHostScheme'
  return null
}

/**
 * Chat transport endpoint for the remote tier: null when the profile is
 * disabled or the host is empty (the caller falls back to the local service),
 * otherwise the direct ChatEndpoint carrying the peer's port and optional API
 * key. Pure mapping, no validation duplication (Settings validates the host
 * before saving; the persisted value is trusted here).
 */
export function remoteEndpoint(p: RemoteChatProfile): ChatEndpoint | null {
  if (!p.enabled || !p.host) return null
  return { host: p.host, port: p.port, apiKey: p.apiKey }
}
