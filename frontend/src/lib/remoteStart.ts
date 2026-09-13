// Remote service start (Phase R): the phone-side client for the PC control
// plane's POST /start endpoint. When the chat page's remote /models probe
// fails because the PC's llama-server is simply not running yet, the warning
// bar offers a one-tap start: the PC's MyLlama app (which owns the control
// plane on port 1900) then brings llama-server up exactly like its own
// "start service" button would. Kept a standalone lib module (no Vue, no
// Wails imports) so the result mapping stays unit-testable like lib/chat.ts.

/**
 * The fixed control-plane port. v1 keeps it a convention — the phone derives
 * the control-plane URL from the pairing host plus this constant (a future
 * pairing-format version may carry an explicit ctrl field).
 */
export const REMOTE_START_PORT = 1900

/** Probe deadline when the caller passes no AbortSignal (LAN RTT is ms-scale). */
const REMOTE_START_TIMEOUT_MS = 5000

/**
 * Outcome of a remote-start attempt:
 *   - started         — the PC accepted and began bringing llama-server up;
 *   - already-running — the PC reports the service already runs (409);
 *   - not-allowed     — auth/permission refusal (401/403): remote start not
 *                       enabled on the PC or the API key does not match;
 *   - unreachable     — the control plane could not be reached at all
 *                       (connection refused or timeout — the PC's MyLlama
 *                       app is not running, or the address is wrong);
 *   - not-running     — reserved: currently unified into 'unreachable'
 *                       (distinguishing refused-vs-timeout needs timing
 *                       heuristics that misclassify on slow LANs);
 *   - failed          — the PC answered but the start attempt failed (5xx)
 *                       or the request was malformed locally.
 */
export type RemoteStartResult =
  | 'started'
  | 'already-running'
  | 'unreachable'
  | 'not-running'
  | 'not-allowed'
  | 'failed'

/**
 * Ask the PC's control plane to start its llama-server: POST
 * http://<host>:1900/start with the pairing API key as the X-Control-Token
 * bearer (the control plane accepts the env token OR this key). Without a
 * caller signal a 5s abort deadline applies (a LAN peer answers in
 * milliseconds; anything slower means the app is not there). The status code
 * alone carries the outcome — the body is not parsed.
 */
export async function remoteStartService(host: string, apiKey: string, signal?: AbortSignal): Promise<RemoteStartResult> {
  const trimmed = host.trim()
  if (!trimmed) return 'failed'
  const controller = new AbortController()
  const timeoutId = signal
    ? null
    : setTimeout(() => controller.abort(), REMOTE_START_TIMEOUT_MS)
  let res: Response
  try {
    res = await fetch(`http://${trimmed}:${REMOTE_START_PORT}/start`, {
      method: 'POST',
      headers: { 'X-Control-Token': apiKey },
      signal: signal ?? controller.signal,
    })
  } catch (e) {
    // Unreachable in every network-layer shape: connection refused (PC app
    // not running), DNS failure, or the 5s abort deadline. Deliberately NOT
    // split into refused-vs-timeout — timing-based classification misfires
    // on loaded LANs (see RemoteStartResult doc).
    void e
    return 'unreachable'
  } finally {
    if (timeoutId !== null) clearTimeout(timeoutId)
  }
  if (res.status === 200) return 'started'
  if (res.status === 409) return 'already-running'
  if (res.status === 401 || res.status === 403) return 'not-allowed'
  return 'failed'
}
