import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest'
import {
  updateState,
  checkForUpdate,
  startUpdateDownload,
  cancelUpdateDownload,
  installUpdate,
  shouldAutoCheck,
  closeUpdateModal,
  stopPolling,
  extractReleaseNotes,
  checkSubmittedAndroidInstall,
  compareDottedVersions,
  CHECK_INTERVAL_MS,
} from '../lib/update'
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

// mock Wails bridge: window.go is injected by Wails runtime only, unavailable in test env
vi.mock('../wails', () => ({
  checkForUpdate: vi.fn(),
  startUpdateDownload: vi.fn(),
  stopUpdateDownload: vi.fn(),
  installUpdate: vi.fn(),
  getUpdateDownloadStatus: vi.fn(),
  installAndroidUpdateApk: vi.fn(),
  openAndroidInstallPermissionSettings: vi.fn(),
  androidAppInfo: vi.fn(),
}))

// mock the Wails runtime event bus: lib/update subscribes to the native
// android:installStatus push at module load
vi.mock('@wailsio/runtime', () => ({
  Events: { On: vi.fn(() => () => {}) },
}))

const mockCheckForUpdate = vi.mocked(checkForUpdateBackend)
const mockStartUpdateDownload = vi.mocked(startUpdateDownloadBackend)
const mockStopUpdateDownload = vi.mocked(stopUpdateDownloadBackend)
const mockInstallUpdate = vi.mocked(installUpdateBackend)
const mockGetStatus = vi.mocked(getUpdateDownloadStatus)
const mockInstallAndroidApk = vi.mocked(installAndroidUpdateApk)
const mockOpenInstallSettings = vi.mocked(openAndroidInstallPermissionSettings)
const mockAndroidAppInfo = vi.mocked(androidAppInfo)

// lib/update subscribes to android:installStatus once at module load; grab
// the handler up front — beforeEach resetAllMocks clears mock.calls but the
// captured reference keeps working.
const onInstallStatus = ((Events.On as unknown as Mock).mock.calls.find(
  (c) => c[0] === 'android:installStatus',
)?.[1] ?? (() => {})) as (raw: unknown) => void

const SUBMITTED_KEY = 'myllama-update-submitted'

function resetState() {
  updateState.checking = false
  updateState.result = null
  updateState.download = null
  updateState.showModal = false
  updateState.error = ''
  updateState.installing = false
  updateState.installError = ''
  updateState.androidInstallSubmitted = false
}

describe('lib/update', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.useRealTimers()
    localStorage.clear()
    resetState()
  })

  afterEach(() => {
    // stopPolling instead of closeUpdateModal: closing no longer stops polling
    // while a download is in progress (background download support)
    stopPolling()
    vi.useRealTimers()
  })

  it('shouldAutoCheck: never checked returns true', () => {
    expect(shouldAutoCheck()).toBe(true)
  })

  it('shouldAutoCheck: less than 48h since last check returns false', () => {
    localStorage.setItem('llama-desktop-last-update-check', String(Date.now() - 60 * 60 * 1000))
    expect(shouldAutoCheck()).toBe(false)
  })

  it('shouldAutoCheck: more than 48h since last check returns true', () => {
    localStorage.setItem('llama-desktop-last-update-check', String(Date.now() - CHECK_INTERVAL_MS - 1000))
    expect(shouldAutoCheck()).toBe(true)
  })

  it('shouldAutoCheck: falls back to old key when new key missing (llama-gui rename migration); old install throttle not reset', () => {
    localStorage.setItem('llama-gui-last-update-check', String(Date.now() - 60 * 60 * 1000))
    expect(shouldAutoCheck()).toBe(false)
  })

  it('checkForUpdate discovers new version, writes check time, and opens update modal', async () => {
    mockCheckForUpdate.mockResolvedValue({
      hasUpdate: true,
      version: 'v0.2.0',
      notes: '新增功能',
      published: '2026-08-10T00:00:00Z',
    })

    await checkForUpdate()

    expect(updateState.result?.hasUpdate).toBe(true)
    expect(updateState.result?.version).toBe('v0.2.0')
    expect(updateState.showModal).toBe(true)
    expect(updateState.checking).toBe(false)
    // refresh last check time after completion to avoid repeated auto-check on next launch
    expect(localStorage.getItem('llama-desktop-last-update-check')).not.toBeNull()
  })

  it('checkForUpdate no new version: no modal but records check time', async () => {
    mockCheckForUpdate.mockResolvedValue({ hasUpdate: false, version: 'v0.1.0', notes: '', published: '' })

    await checkForUpdate()

    expect(updateState.showModal).toBe(false)
    expect(localStorage.getItem('llama-desktop-last-update-check')).not.toBeNull()
  })

  it('checkForUpdate failure sets error silently, no modal', async () => {
    mockCheckForUpdate.mockRejectedValue(new Error('network down'))

    await checkForUpdate()

    expect(updateState.error).toBe('检查更新失败，请确认网络后重试')
    expect(updateState.showModal).toBe(false)
    expect(updateState.checking).toBe(false)
  })

  it('startUpdateDownload starts download and polls progress, stops after completion', async () => {
    vi.useFakeTimers()
    mockCheckForUpdate.mockResolvedValue({ hasUpdate: true, version: 'v0.2.0', notes: '', published: '' })
    mockStartUpdateDownload.mockResolvedValue(undefined)
    mockGetStatus.mockResolvedValue({
      status: 'done', progress: 100, total: 100, downloaded: 100,
      version: 'v0.2.0', filePath: 'C:/app/MyLlama-portable-v0.2.0.exe', error: '', kind: 'portable', installer: false,
    })

    await checkForUpdate()
    startUpdateDownload()

    expect(mockStartUpdateDownload).toHaveBeenCalledWith('v0.2.0')
    expect(updateState.download?.status).toBe('downloading')

    await vi.advanceTimersByTimeAsync(1100) // trigger one poll
    expect(updateState.download?.status).toBe('done')
    expect(updateState.download?.filePath).toContain('MyLlama-portable-v0.2.0.exe')

    // polling should stop after completion (advancing time should not repeat fetches)
    const callsAfterDone = mockGetStatus.mock.calls.length
    await vi.advanceTimersByTimeAsync(2200)
    expect(mockGetStatus.mock.calls.length).toBe(callsAfterDone)
  })

  it('closeUpdateModal hides the modal (no download in progress)', () => {
    updateState.showModal = true
    closeUpdateModal()
    expect(updateState.showModal).toBe(false)
  })

  it('closeUpdateModal while downloading keeps polling in the background', async () => {
    vi.useFakeTimers()
    mockCheckForUpdate.mockResolvedValue({ hasUpdate: true, version: 'v0.2.0', notes: '', published: '' })
    mockStartUpdateDownload.mockResolvedValue(undefined)
    mockGetStatus.mockResolvedValue({
      status: 'downloading', progress: 40, total: 1000, downloaded: 400,
      version: 'v0.2.0', filePath: '', error: '', kind: 'portable', installer: false,
    })

    await checkForUpdate()
    startUpdateDownload()
    await vi.advanceTimersByTimeAsync(1100) // one poll
    expect(updateState.download?.status).toBe('downloading')

    closeUpdateModal()
    expect(updateState.showModal).toBe(false)
    // download state kept so the dock background row stays live
    expect(updateState.download?.status).toBe('downloading')

    // polling continues after close
    const callsAfterClose = mockGetStatus.mock.calls.length
    await vi.advanceTimersByTimeAsync(2100)
    expect(mockGetStatus.mock.calls.length).toBeGreaterThan(callsAfterClose)
  })

  it('closeUpdateModal when done stops polling and clears the download state', async () => {
    vi.useFakeTimers()
    mockCheckForUpdate.mockResolvedValue({ hasUpdate: true, version: 'v0.2.0', notes: '', published: '' })
    mockStartUpdateDownload.mockResolvedValue(undefined)
    mockGetStatus.mockResolvedValue({
      status: 'done', progress: 100, total: 100, downloaded: 100,
      version: 'v0.2.0', filePath: 'C:/app/MyLlama-portable-v0.2.0.exe', error: '', kind: 'portable', installer: false,
    })

    await checkForUpdate()
    startUpdateDownload()
    await vi.advanceTimersByTimeAsync(1100) // one poll completes the download
    expect(updateState.download?.status).toBe('done')

    closeUpdateModal()
    expect(updateState.showModal).toBe(false)
    // terminal state cleared so the dock row does not linger
    expect(updateState.download).toBeNull()

    // polling stopped: no further status fetches
    const callsAfterClose = mockGetStatus.mock.calls.length
    await vi.advanceTimersByTimeAsync(2200)
    expect(mockGetStatus.mock.calls.length).toBe(callsAfterClose)
  })

  it('closeUpdateModal when errored stops polling and clears the download state', async () => {
    vi.useFakeTimers()
    mockCheckForUpdate.mockResolvedValue({ hasUpdate: true, version: 'v0.2.0', notes: '', published: '' })
    mockStartUpdateDownload.mockResolvedValue(undefined)
    mockGetStatus.mockResolvedValue({
      status: 'error', progress: 0, total: 0, downloaded: 0,
      version: 'v0.2.0', filePath: '', error: 'network reset', kind: '', installer: false,
    })

    await checkForUpdate()
    startUpdateDownload()
    await vi.advanceTimersByTimeAsync(1100) // one poll surfaces the error
    expect(updateState.download?.status).toBe('error')

    closeUpdateModal()
    expect(updateState.download).toBeNull()

    const callsAfterClose = mockGetStatus.mock.calls.length
    await vi.advanceTimersByTimeAsync(2200)
    expect(mockGetStatus.mock.calls.length).toBe(callsAfterClose)
  })

  it('cancelUpdateDownload stops the backend download and polling, clears state, keeps modal on confirm view', async () => {
    vi.useFakeTimers()
    mockCheckForUpdate.mockResolvedValue({ hasUpdate: true, version: 'v0.2.0', notes: '', published: '' })
    mockStartUpdateDownload.mockResolvedValue(undefined)
    mockGetStatus.mockResolvedValue({
      status: 'downloading', progress: 60, total: 1000, downloaded: 600,
      version: 'v0.2.0', filePath: '', error: '', kind: 'portable', installer: false,
    })
    mockStopUpdateDownload.mockResolvedValue(undefined)

    await checkForUpdate()
    startUpdateDownload()
    await vi.advanceTimersByTimeAsync(1100) // one poll

    await cancelUpdateDownload()

    expect(mockStopUpdateDownload).toHaveBeenCalledTimes(1)
    expect(updateState.download).toBeNull()
    // modal stays open with the result intact: user lands back on the confirm view
    expect(updateState.showModal).toBe(true)
    expect(updateState.result?.version).toBe('v0.2.0')

    // polling stopped: no further status fetches
    const callsAfterCancel = mockGetStatus.mock.calls.length
    await vi.advanceTimersByTimeAsync(2200)
    expect(mockGetStatus.mock.calls.length).toBe(callsAfterCancel)
  })

  it('cancelUpdateDownload swallows backend stop failures', async () => {
    mockStopUpdateDownload.mockRejectedValue(new Error('backend busy'))
    updateState.download = {
      status: 'downloading', progress: 10, total: 1000, downloaded: 100,
      version: 'v0.2.0', filePath: '', error: '', kind: 'portable', installer: false,
    }

    await expect(cancelUpdateDownload()).resolves.toBeUndefined()
    expect(updateState.download).toBeNull()
  })

  it('installUpdate calls the backend and keeps installing state (app about to exit)', async () => {
    updateState.download = {
      status: 'done', progress: 100, total: 100, downloaded: 100,
      version: 'v0.2.0', filePath: 'C:/app/MyLlama-setup-v0.2.0.exe', error: '', kind: 'setup', installer: true,
    }
    mockInstallUpdate.mockResolvedValue(undefined)

    await installUpdate()

    expect(mockInstallUpdate).toHaveBeenCalledTimes(1)
    // installing stays true: the backend launches the installer and the app exits
    expect(updateState.installing).toBe(true)
    expect(updateState.installError).toBe('')
  })

  it('installUpdate failure resets installing and records the error for retry', async () => {
    updateState.download = {
      status: 'done', progress: 100, total: 100, downloaded: 100,
      version: 'v0.2.0', filePath: 'C:/app/MyLlama-setup-v0.2.0.exe', error: '', kind: 'setup', installer: true,
    }
    mockInstallUpdate.mockRejectedValue(new Error('no such file'))

    await installUpdate()

    expect(updateState.installing).toBe(false)
    expect(updateState.installError).toBe('启动安装器失败：no such file')
  })

  it('android kind hands the apk path to the Java bridge and marks the install submitted', async () => {
    updateState.download = {
      status: 'done', progress: 100, total: 100, downloaded: 100,
      version: 'v0.2.0', filePath: '/data/data/com.codeneow.llamadesktop/files/MyLlama-android-v0.2.0.apk',
      error: '', kind: 'android', installer: true,
    }
    mockInstallAndroidApk.mockResolvedValue(undefined)

    await installUpdate()

    expect(mockInstallAndroidApk).toHaveBeenCalledWith(updateState.download?.filePath)
    // the desktop Go installer flow must not be touched on android
    expect(mockInstallUpdate).not.toHaveBeenCalled()
    // the app does not exit: no installing state, the submitted tip shows instead
    expect(updateState.installing).toBe(false)
    expect(updateState.androidInstallSubmitted).toBe(true)
    expect(updateState.installError).toBe('')
  })

  it('android needInstallPermission opens the Settings screen and surfaces the recovery hint', async () => {
    updateState.download = {
      status: 'done', progress: 100, total: 100, downloaded: 100,
      version: 'v0.2.0', filePath: '/data/data/com.codeneow.llamadesktop/files/MyLlama-android-v0.2.0.apk',
      error: '', kind: 'android', installer: true,
    }
    mockInstallAndroidApk.mockRejectedValue(Object.assign(new Error('missing grant'), { code: 'needInstallPermission' }))
    mockOpenInstallSettings.mockResolvedValue(undefined)

    await installUpdate()

    expect(mockOpenInstallSettings).toHaveBeenCalledTimes(1)
    expect(updateState.androidInstallSubmitted).toBe(false)
    expect(updateState.installError).toContain('安装未知应用')
  })

  it('android generic install failure surfaces the error without opening Settings', async () => {
    updateState.download = {
      status: 'done', progress: 100, total: 100, downloaded: 100,
      version: 'v0.2.0', filePath: '/data/data/com.codeneow.llamadesktop/files/MyLlama-android-v0.2.0.apk',
      error: '', kind: 'android', installer: true,
    }
    mockInstallAndroidApk.mockRejectedValue(new Error('session commit failed'))

    await installUpdate()

    expect(mockOpenInstallSettings).not.toHaveBeenCalled()
    expect(updateState.installError).toBe('安装失败：session commit failed')
  })

  it('android submit persists the submitted marker with the target version', async () => {
    mockCheckForUpdate.mockResolvedValue({ hasUpdate: true, version: 'v0.2.0', notes: '', published: '' })
    await checkForUpdate()
    updateState.download = {
      status: 'done', progress: 100, total: 100, downloaded: 100,
      version: 'v0.2.0', filePath: '/data/data/com.codeneow.llamadesktop/files/MyLlama-android-v0.2.0.apk',
      error: '', kind: 'android', installer: true,
    }
    mockInstallAndroidApk.mockResolvedValue(undefined)

    await installUpdate()

    expect(updateState.androidInstallSubmitted).toBe(true)
    const raw = localStorage.getItem(SUBMITTED_KEY)
    expect(raw).not.toBeNull()
    const marker = JSON.parse(raw!) as { version: string; at: number }
    expect(marker.version).toBe('v0.2.0')
    expect(typeof marker.at).toBe('number')
  })

  it('android:installStatus failure event surfaces the installer message', () => {
    onInstallStatus({
      name: 'android:installStatus',
      data: JSON.stringify({ status: 'failure', message: 'INSTALL_FAILED_INVALID_URI' }),
    })
    expect(updateState.installError).toBe('安装失败：INSTALL_FAILED_INVALID_URI')
  })

  it('android:installStatus success event clears the submitted marker', () => {
    localStorage.setItem(SUBMITTED_KEY, JSON.stringify({ version: 'v0.2.0', at: Date.now() }))
    onInstallStatus({
      name: 'android:installStatus',
      data: JSON.stringify({ status: 'success', message: '' }),
    })
    expect(localStorage.getItem(SUBMITTED_KEY)).toBeNull()
  })

  it('checkSubmittedAndroidInstall clears the marker once the running version caught up (v-prefix tolerant)', async () => {
    localStorage.setItem(SUBMITTED_KEY, JSON.stringify({ version: 'v0.2.0', at: 1 }))
    mockAndroidAppInfo.mockResolvedValue({
      name: 'MyLlama', version: '0.2.0', build: '20', bundleId: 'com.codeneow.llamadesktop',
    })

    await checkSubmittedAndroidInstall()

    expect(localStorage.getItem(SUBMITTED_KEY)).toBeNull()
  })

  it('checkSubmittedAndroidInstall keeps the marker while the running version is older', async () => {
    localStorage.setItem(SUBMITTED_KEY, JSON.stringify({ version: 'v0.2.0', at: 1 }))
    mockAndroidAppInfo.mockResolvedValue({
      name: 'MyLlama', version: '0.1.9', build: '19', bundleId: 'com.codeneow.llamadesktop',
    })

    await checkSubmittedAndroidInstall()

    expect(localStorage.getItem(SUBMITTED_KEY)).not.toBeNull()
  })

  it('checkSubmittedAndroidInstall compares segments numerically (0.2.10 > 0.2.9)', async () => {
    localStorage.setItem(SUBMITTED_KEY, JSON.stringify({ version: '0.2.9', at: 1 }))
    mockAndroidAppInfo.mockResolvedValue({
      name: 'MyLlama', version: '0.2.10', build: '30', bundleId: 'com.codeneow.llamadesktop',
    })

    await checkSubmittedAndroidInstall()

    expect(localStorage.getItem(SUBMITTED_KEY)).toBeNull()
  })

  it('checkSubmittedAndroidInstall leaves the marker untouched without the bridge (desktop / dev)', async () => {
    localStorage.setItem(SUBMITTED_KEY, JSON.stringify({ version: 'v0.2.0', at: 1 }))
    mockAndroidAppInfo.mockResolvedValue(null)

    await checkSubmittedAndroidInstall()

    expect(localStorage.getItem(SUBMITTED_KEY)).not.toBeNull()
  })

  it('checkSubmittedAndroidInstall is a no-op without a marker', async () => {
    mockAndroidAppInfo.mockResolvedValue({
      name: 'MyLlama', version: '9.9.9', build: '999', bundleId: 'com.codeneow.llamadesktop',
    })

    await expect(checkSubmittedAndroidInstall()).resolves.toBeUndefined()
    expect(mockAndroidAppInfo).not.toHaveBeenCalled()
    expect(localStorage.getItem(SUBMITTED_KEY)).toBeNull()
  })
})

// ─── extractReleaseNotes (bilingual release notes) ─────────────────────────

// Historical body shape (v0.2.7 – v0.3.2): English section first
const EN_FIRST_BODY = `## English

v0.2.7: Fix the blank API page. Core changes in English here.

1. fix(frontend): guard null gpus
   - Core: English detail.

## 中文

v0.2.7: 修复 API 页空白。核心改动中文说明。

1. fix(frontend): API 页监控渲染对 null gpus 防御
   - 核心：中文细节。`

// Current body shape (v0.3.3+): Chinese section first
const ZH_FIRST_BODY = `## 中文

v0.3.3: 页面固定布局与模型 ID 统一。核心改动中文说明。

## English

v0.3.3: Fixed-viewport pages and unified model IDs. English detail here.`

describe('extractReleaseNotes', () => {
  it('zh locale returns only the Chinese section (English-first body)', () => {
    const zh = extractReleaseNotes(EN_FIRST_BODY, 'zh')
    expect(zh).toContain('修复 API 页空白')
    expect(zh).toContain('中文细节')
    expect(zh).not.toContain('English')
  })

  it('en locale returns only the English section (English-first body)', () => {
    const en = extractReleaseNotes(EN_FIRST_BODY, 'en')
    expect(en).toContain('Fix the blank API page')
    expect(en).toContain('English detail')
    expect(en).not.toContain('中文')
    expect(en).not.toContain('修复 API 页空白')
  })

  it('zh locale returns only the Chinese section (Chinese-first body)', () => {
    const zh = extractReleaseNotes(ZH_FIRST_BODY, 'zh')
    expect(zh).toContain('核心改动中文说明')
    expect(zh).not.toContain('English')
    expect(zh).not.toContain('unified model IDs')
  })

  it('en locale returns only the English section (Chinese-first body)', () => {
    const en = extractReleaseNotes(ZH_FIRST_BODY, 'en')
    expect(en).toContain('English detail here')
    expect(en).not.toContain('中文')
    expect(en).not.toContain('核心改动')
  })

  it('body without markers falls back to the full text (historical releases)', () => {
    const legacy = 'Legacy single-language notes'
    expect(extractReleaseNotes(legacy, 'zh')).toBe(legacy)
    expect(extractReleaseNotes(legacy, 'en')).toBe(legacy)
  })

  it('empty body returns empty string', () => {
    expect(extractReleaseNotes('', 'zh')).toBe('')
    expect(extractReleaseNotes('', 'en')).toBe('')
  })
})

// ─── compareDottedVersions (Android submitted-install reconciler) ───────────

describe('compareDottedVersions', () => {
  it('returns 0 for equal versions (v-prefix tolerant)', () => {
    expect(compareDottedVersions('0.2.0', 'v0.2.0')).toBe(0)
    expect(compareDottedVersions('1.0', '1.0.0')).toBe(0)
  })

  it('compares segments numerically, not lexicographically', () => {
    expect(compareDottedVersions('0.2.10', '0.2.9')).toBeGreaterThan(0)
    expect(compareDottedVersions('0.2.9', '0.2.10')).toBeLessThan(0)
    expect(compareDottedVersions('0.10.0', '0.9.0')).toBeGreaterThan(0)
  })

  it('orders by the first differing segment', () => {
    expect(compareDottedVersions('0.3.0', '0.2.99')).toBeGreaterThan(0)
    expect(compareDottedVersions('1.0.0', '0.9.9')).toBeGreaterThan(0)
  })
})
