import { reactive } from 'vue'
import {
  checkForUpdate as checkForUpdateBackend,
  startUpdateDownload as startUpdateDownloadBackend,
  stopUpdateDownload as stopUpdateDownloadBackend,
  installUpdate as installUpdateBackend,
  getUpdateDownloadStatus,
  installAndroidUpdateApk,
  openAndroidInstallPermissionSettings,
  androidAppInfo,
} from '../wails'
import { Events } from '@wailsio/runtime'
import { t } from './i18n'

// Auto-check throttle: skip auto-check within 48 hours of the last check (local time).
// Manual checks are not throttled.
export const CHECK_INTERVAL_MS = 48 * 60 * 60 * 1000
const CHECK_KEY = 'llama-desktop-last-update-check'
// Legacy key from before the llama-gui → llama-desktop rename: read-only fallback
// preserving the throttle timestamp of older installs
const LEGACY_CHECK_KEY = 'llama-gui-last-update-check'

export interface UpdateResult {
  hasUpdate: boolean
  version: string
  notes: string
  published: string
}

export interface UpdateDownloadState {
  status: string // idle / downloading / installing / done / error
  progress: number
  total: number
  downloaded: number
  version: string
  filePath: string
  error: string
  kind: string // Install kind of the running app: setup (NSIS install) / portable / android (apk via the system installer)
  installer: boolean // Whether the downloaded artifact is the setup installer (install-now flow)
}

/** Markers delimiting the bilingual release-notes segments. Since v0.3.3 the
 *  Chinese section comes first and the English section second; historical
 *  bodies (v0.2.7 – v0.3.2) carry English first. The extraction is
 *  order-agnostic. The UI shows only the section matching the locale. */
const NOTES_EN_MARKER = '## English'
const NOTES_ZH_MARKER = '## 中文'

/**
 * Extract the release-notes section matching the UI language.
 *
 * Release bodies carry both an English and a Chinese section, each introduced
 * by its marker (order varies across releases). The section runs from its
 * marker to the other marker or the end of the body. Bodies without markers
 * (historical releases / unusual bodies) fall back to the full text,
 * preserving the previous behavior.
 */
export function extractReleaseNotes(body: string, lang: 'zh' | 'en'): string {
  const trimmed = body.trim()
  const zhIdx = trimmed.indexOf(NOTES_ZH_MARKER)
  const enIdx = trimmed.indexOf(NOTES_EN_MARKER)
  const marker = lang === 'zh' ? NOTES_ZH_MARKER : NOTES_EN_MARKER
  const start = trimmed.indexOf(marker)
  if (start < 0) return trimmed
  const ends = [zhIdx, enIdx].filter((i) => i > start)
  const end = ends.length > 0 ? Math.min(...ends) : trimmed.length
  return trimmed.slice(start + marker.length, end).trim()
}

export const updateState = reactive({
  checking: false,
  result: null as UpdateResult | null,
  download: null as UpdateDownloadState | null,
  showModal: false,
  error: '',
  installing: false, // install-now confirmed and accepted by the backend; the app is exiting
  installError: '', // install-now launch failure shown in the modal (retry stays possible)
  // Android: the APK was handed to the system installer (the dialog is
  // cancellable, so the "install now" button stays usable for a re-trigger)
  androidInstallSubmitted: false,
})

let downloadTimer: ReturnType<typeof setInterval> | null = null

// ─── Android install-status feedback (system PackageInstaller) ──────────────

// Event MainActivity/WailsBridge emit after an APK install commit: the payload
// is {"status":"success"|"failure","message":"..."} (JSON string). The first
// PackageInstaller delivery (STATUS_PENDING_USER_ACTION) never reaches JS —
// the Java bridge starts the system confirmation dialog from it directly.
const ANDROID_INSTALL_STATUS_EVENT = 'android:installStatus'

// Remembers that an Android APK install was handed to the system installer and
// for which target version: a successful install kills and replaces the
// process, so a "success" event can never arrive in the new process. Instead
// the restarted app clears the marker once its own version has caught up
// (checkSubmittedAndroidInstall); a marker that survives means the install
// never finished and the check-update flow can be re-run.
const SUBMITTED_KEY = 'myllama-update-submitted'

// onInstallStatus applies an "android:installStatus" event. Events.On
// delivers the runtime's WailsEvent wrapper ({name, data}) — NOT the payload
// itself — and the native side emits the payload as a JSON string
// (WailsBridge.emitEvent → JSONObject.toString()). Unwrap and parse both
// layers; bare payloads (unit tests) keep working. Mirrors lib/safeArea.ts
// onPush.
function onInstallStatus(raw: unknown): void {
  let payload: unknown = raw
  if (payload !== null && typeof payload === 'object' && 'data' in (payload as Record<string, unknown>)) {
    payload = (payload as { data?: unknown }).data
  }
  if (typeof payload === 'string') {
    try {
      payload = JSON.parse(payload)
    } catch {
      payload = {}
    }
  }
  const src = (payload && typeof payload === 'object' ? payload : {}) as Record<string, unknown>
  if (src.status === 'success') {
    // Install confirmed by the system: the submitted marker did its job.
    try {
      localStorage.removeItem(SUBMITTED_KEY)
    } catch {
      // localStorage unavailable: nothing to clean.
    }
  } else if (src.status === 'failure') {
    const message = typeof src.message === 'string' ? src.message : ''
    updateState.installError = t('updateModal.androidInstallFailed', { msg: message })
  }
}

let installStatusListenerInitialized = false

// initAndroidInstallStatusListener wires the native install-status push.
// Idempotent; a runtime without event support (plain vite) degrades to a
// no-op. Called once below — equivalent to a module-scope subscription, so
// App.vue needs no extra wiring.
export function initAndroidInstallStatusListener(): void {
  if (installStatusListenerInitialized) return
  installStatusListenerInitialized = true
  try {
    Events.On(ANDROID_INSTALL_STATUS_EVENT, onInstallStatus)
  } catch {
    // Runtime without event support: nothing to subscribe to.
  }
}
initAndroidInstallStatusListener()

/** Whether more than 48 hours have passed since the last check (or it never happened). */
export function shouldAutoCheck(now = Date.now()): boolean {
  // Fall back to the legacy key when the new key is missing (rename migration), so
  // older installs do not get their throttle window reset into a duplicate check prompt
  const last = Number(localStorage.getItem(CHECK_KEY) || localStorage.getItem(LEGACY_CHECK_KEY) || 0)
  if (!last) return true
  return now - last > CHECK_INTERVAL_MS
}

function writeCheckTime(now = Date.now()) {
  localStorage.setItem(CHECK_KEY, String(now))
}

/**
 * Check for a new version. The last-check time is refreshed once the check
 * completes (regardless of outcome or failure), avoiding "just checked manually,
 * auto-check fires again on next launch". Shows the update modal when a new
 * version is found.
 */
export async function checkForUpdate(): Promise<void> {
  updateState.checking = true
  updateState.error = ''
  try {
    const result = await checkForUpdateBackend()
    writeCheckTime()
    updateState.result = {
      hasUpdate: result.hasUpdate,
      version: result.version,
      notes: result.notes || '',
      published: result.published || '',
    }
    // Version comparison is authoritative on the backend currentVersion; here it is display only
    updateState.showModal = result.hasUpdate
  } catch {
    writeCheckTime()
    updateState.error = t('update.checkFailed')
  } finally {
    updateState.checking = false
  }
}

/** User accepted the update: start downloading the new version and poll progress. */
export function startUpdateDownload(): void {
  const version = updateState.result?.version
  if (!version) return
  updateState.download = {
    status: 'downloading',
    progress: 0,
    total: 0,
    downloaded: 0,
    version,
    filePath: '',
    error: '',
    kind: '', // Artifact kind unknown before the download starts; filled from the backend status once polled
    installer: false, // Filled from the backend status once the asset has been picked
  }
  startUpdateDownloadBackend(version).catch(() => {
    if (updateState.download) {
      updateState.download.status = 'error'
      updateState.download.error = t('update.startFailed')
    }
    stopPolling()
  })
  if (downloadTimer) clearInterval(downloadTimer)
  downloadTimer = setInterval(() => pollUpdateDownload(), 1000)
}

/** Fetch the download status once; stop polling when finished. */
export async function pollUpdateDownload(): Promise<void> {
  try {
    const st = await getUpdateDownloadStatus()
    updateState.download = st
    if (st.status === 'done' || st.status === 'error') stopPolling()
  } catch {
    stopPolling()
  }
}

export function stopPolling(): void {
  if (downloadTimer) {
    clearInterval(downloadTimer)
    downloadTimer = null
  }
}

/**
 * Close the update modal. While still downloading, polling keeps running so the
 * background progress row (TaskDock) stays live. Otherwise polling stops and a
 * terminal download state (done/error) is cleared so the dock row does not linger.
 */
export function closeUpdateModal(): void {
  updateState.showModal = false
  if (updateState.download?.status === 'downloading') return
  stopPolling()
  const st = updateState.download?.status
  if (st === 'done' || st === 'error') updateState.download = null
  // Also clear install-now leftovers (e.g. a launch failure) so a later
  // download starts from a clean slate
  updateState.installing = false
  updateState.installError = ''
  updateState.androidInstallSubmitted = false
}

/**
 * User cancelled the download: stop the backend download and the polling, clear
 * the download state, and keep the modal open so the user lands back on the
 * confirm view (updateState.result is untouched). Backend failures are swallowed:
 * the UI has already reset, so there is nothing meaningful left to surface.
 */
export async function cancelUpdateDownload(): Promise<void> {
  stopPolling()
  updateState.download = null
  try {
    await stopUpdateDownloadBackend()
  } catch {
    // backend already idle or unavailable: ignore
  }
}

/**
 * User confirmed "install now": on desktop the backend launches the downloaded
 * setup installer and exits the app shortly after; on Android the APK is handed
 * to the system package installer through the Java bridge and the system
 * confirmation dialog takes over (the app stays usable, the dialog is
 * cancellable and can be re-triggered). On rejection the error surfaces in the
 * modal so the user can retry or update manually.
 */
export async function installUpdate(): Promise<void> {
  updateState.installError = ''
  if (updateState.download?.kind === 'android') {
    await installUpdateAndroid()
    return
  }
  updateState.installing = true
  try {
    await installUpdateBackend()
  } catch (e) {
    updateState.installing = false
    updateState.installError = t('updateModal.installFailed', { msg: e instanceof Error ? e.message : String(e) })
  }
}

/**
 * Android install-now: hand the downloaded APK to the system package installer
 * via the Java bridge. When the "install unknown apps" grant is missing, the
 * Settings screen is opened for the user and the modal shows the recovery
 * hint; a successful commit flips the done-view tip to the submitted state
 * (the system dialog is cancellable, so re-triggering stays possible) and
 * records the submitted-install marker for the post-update reconciler.
 */
async function installUpdateAndroid(): Promise<void> {
  const path = updateState.download?.filePath
  if (!path) {
    updateState.installError = t('updateModal.installFailed', { msg: 'apk path missing' })
    return
  }
  try {
    await installAndroidUpdateApk(path)
    writeSubmitMarker(updateState.result?.version ?? '')
    updateState.androidInstallSubmitted = true
  } catch (e) {
    const code = (e as Error & { code?: string }).code
    if (code === 'needInstallPermission') {
      await openAndroidInstallPermissionSettings()
      updateState.installError = t('updateModal.needInstallPermission')
    } else {
      updateState.installError = t('updateModal.androidInstallFailed', {
        msg: e instanceof Error ? e.message : String(e),
      })
    }
  }
}

// writeSubmitMarker records the pending install (target version + time) so
// the post-update process can tell a completed install from an abandoned one.
function writeSubmitMarker(version: string): void {
  try {
    localStorage.setItem(SUBMITTED_KEY, JSON.stringify({ version, at: Date.now() }))
  } catch {
    // localStorage unavailable: best-effort marker only.
  }
}

// readSubmitMarker returns the target version recorded with the last
// submitted install, or null when absent/unreadable (treated as no marker).
function readSubmitMarker(): { version: string } | null {
  try {
    const raw = localStorage.getItem(SUBMITTED_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<{ version: unknown }>
    return typeof parsed.version === 'string' && parsed.version ? { version: parsed.version } : null
  } catch {
    return null
  }
}

/**
 * Compare two dotted numeric version strings ("0.2.10" vs "0.2.9"), ignoring
 * a leading "v" (release tags) and reading non-numeric segments as 0. Returns
 * a positive number when a > b, 0 when equal, negative when a < b.
 */
export function compareDottedVersions(a: string, b: string): number {
  const pa = a.replace(/^v/i, '').split('.')
  const pb = b.replace(/^v/i, '').split('.')
  const len = Math.max(pa.length, pb.length)
  for (let i = 0; i < len; i++) {
    const na = Number(pa[i]) || 0
    const nb = Number(pb[i]) || 0
    if (na !== nb) return na - nb
  }
  return 0
}

/**
 * Reconcile a previously submitted Android install after a restart: when the
 * running version has reached the submitted marker's version, the update was
 * completed and the marker is cleared; an older running version means the
 * install never finished (dialog dismissed, process killed early), so the
 * marker stays and the user can re-run the check-update flow. No marker, no
 * bridge (desktop / dev) or an unreadable version are no-ops that leave the
 * marker untouched.
 */
export async function checkSubmittedAndroidInstall(): Promise<void> {
  const marker = readSubmitMarker()
  if (!marker) return
  try {
    const info = await androidAppInfo()
    if (!info?.version) return
    if (compareDottedVersions(info.version, marker.version) >= 0) {
      try {
        localStorage.removeItem(SUBMITTED_KEY)
      } catch {
        // localStorage unavailable: nothing to clean.
      }
    }
  } catch {
    // Bridge call failed: keep the marker (version unknown).
  }
}
