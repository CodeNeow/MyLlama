/**
 * Shared llama-server restart routine.
 *
 * The backend StopServer binding returns only after the child process has
 * actually been reaped (bridge.go's bounded exit wait), but the frontend still
 * confirms the stopped state through GetServerStatus before starting again:
 * this keeps the sequence safe against the adopted-server path (no completion
 * handle, kill-by-pid) and makes the helper self-contained for any caller
 * (issue #28: an immediate stop→start used to hit the backend's
 * already-running guard and silently no-op the start half).
 */

import { getServerStatus, startServer, stopServer } from '../wails'

/** Default bounded wait for the status to report "not running" after stop. */
const DEFAULT_STOP_TIMEOUT_MS = 10000

/** Default poll interval while waiting for the stopped status. */
const DEFAULT_POLL_INTERVAL_MS = 250

export interface RestartOptions {
  /** Bounded wait for the stopped status (default 10s); the start half still runs when it lapses. */
  stopTimeoutMs?: number
  /** Poll interval for the status probe (default 250ms). */
  pollIntervalMs?: number
}

/**
 * Restart the llama-server service: stop, wait (bounded) until the status
 * reports not running, then start. Rejects when the stop or the start binding
 * fails; a stop that never confirms within the bounded wait proceeds to the
 * start anyway (the backend's start path owns the final verdict).
 */
export async function restartServer(opts?: RestartOptions): Promise<void> {
  const stopTimeoutMs = opts?.stopTimeoutMs ?? DEFAULT_STOP_TIMEOUT_MS
  const pollIntervalMs = opts?.pollIntervalMs ?? DEFAULT_POLL_INTERVAL_MS

  await stopServer()

  const deadline = Date.now() + stopTimeoutMs
  while (Date.now() < deadline) {
    try {
      const status = await getServerStatus()
      if (!status.running) break
    } catch {
      // Status probe failure (e.g. backend busy): keep polling until the deadline
    }
    await new Promise((resolve) => setTimeout(resolve, pollIntervalMs))
  }

  await startServer()
}
