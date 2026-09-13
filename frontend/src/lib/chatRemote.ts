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

// ─── QR / clipboard pairing payload ──────────────────────────────────────────
// The PC side renders its pairing as a QR code (Settings "LAN Pairing"
// card, share section) and copies it as a link; the phone side scans it (or
// imports it from the clipboard) and fills the remote-chat form. Both
// directions share the single versioned format below.

/**
 * Canonical QR/copy pairing payload: myllama://pair?v=1&host=<enc>&port=<n>[&key=<enc>].
 * The key segment is omitted when empty; v=1 lets future formats coexist.
 * host/apiKey are percent-encoded via URLSearchParams (build and parse use
 * the same codec, so the round trip is exact — including spaces and "+");
 * port is decimal. An empty host or an out-of-range port returns '' so the
 * caller can hide the QR code / disable copy while nothing is addressable.
 */
export function buildPairPayload(p: { host: string; port: number; apiKey: string }): string {
  const host = p.host.trim()
  if (!host) return ''
  if (!Number.isInteger(p.port) || p.port < 1 || p.port > 65535) return ''
  const q = new URLSearchParams({ v: '1', host, port: String(p.port) })
  if (p.apiKey) q.set('key', p.apiKey)
  return `myllama://pair?${q.toString()}`
}

/** The exact prefix a pairing payload must carry (scheme + fixed path + query). */
const PAIR_PREFIX = 'myllama://pair?'

/**
 * Parse a pairing payload (scanned QR text or clipboard content). Accepts
 * exactly the v=1 format built by buildPairPayload: v must be "1", host
 * non-empty without a scheme ("://") or path ("/"), port a decimal integer
 * in 1..65535, key optional (missing/empty → ''). Unknown extra parameters
 * are ignored so scanners appending noise cannot break the import. Returns
 * null on anything else — never throws.
 */
export function parsePairPayload(text: string): { host: string; port: number; apiKey: string } | null {
  const trimmed = text.trim()
  if (!trimmed.startsWith(PAIR_PREFIX)) return null
  // URLSearchParams never throws (it is lenient about malformed percent
  // escapes) and decodes "+" as a space — the same codec buildPairPayload
  // encodes with, so the round trip is exact.
  const q = new URLSearchParams(trimmed.slice(PAIR_PREFIX.length))
  if (q.get('v') !== '1') return null
  const host = (q.get('host') ?? '').trim()
  if (!host || host.includes('://') || host.includes('/')) return null
  const portRaw = (q.get('port') ?? '').trim()
  if (!/^\d+$/.test(portRaw)) return null
  const port = Number(portRaw)
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null
  return { host, port, apiKey: q.get('key') ?? '' }
}
