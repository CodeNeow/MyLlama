import { describe, it, expect, vi, beforeEach } from 'vitest'
import { appConfig, loadConfig, setTheme, setDownloadSource, setLanguage, setServerAccessMode, setApiKey, setTrayEnabled, setSidebarCollapsed, setRemoteChat, readStoredTheme, readStoredSidebarCollapsed } from '../store'
import { getConfig, setTheme as setThemeBackend, setDownloadSource as setDownloadSourceBackend, setLanguage as setLanguageBackend, setTrayEnabled as setTrayEnabledBackend, setSidebarCollapsed as setSidebarCollapsedBackend, getServerConfig, saveServerConfig as saveServerConfigBackend, saveRemoteChat as saveRemoteChatBackend } from '../wails'
import { locale } from '../lib/i18n'

// mock Wails bridge: window.go is injected by Wails runtime only, unavailable in test env
vi.mock('../wails', () => ({
  getConfig: vi.fn(),
  setTheme: vi.fn(),
  setDownloadSource: vi.fn(),
  setLanguage: vi.fn(),
  setTrayEnabled: vi.fn(),
  setSidebarCollapsed: vi.fn(),
  getServerConfig: vi.fn(),
  saveServerConfig: vi.fn(),
  saveRemoteChat: vi.fn(),
}))

const mockGetConfig = vi.mocked(getConfig)
const mockSetTheme = vi.mocked(setThemeBackend)
const mockSetDownloadSource = vi.mocked(setDownloadSourceBackend)
const mockSetLanguage = vi.mocked(setLanguageBackend)
const mockSetTrayEnabled = vi.mocked(setTrayEnabledBackend)
const mockSetSidebarCollapsed = vi.mocked(setSidebarCollapsedBackend)
const mockGetServerConfig = vi.mocked(getServerConfig)
const mockSaveServerConfig = vi.mocked(saveServerConfigBackend)
const mockSaveRemoteChat = vi.mocked(saveRemoteChatBackend)

describe('store', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    localStorage.clear()
    appConfig.theme = 'light'
    appConfig.llamaCppDir = ''
    appConfig.modelsDir = ''
    appConfig.downloadSource = 'hf'
    appConfig.serverAccessMode = 'local'
    appConfig.serverApiKey = ''
    appConfig.language = 'auto'
    appConfig.resolvedLanguage = 'zh'
    appConfig.trayEnabled = true
    appConfig.apiRouteMode = false
    // reset remote-chat pairing to the documented defaults (backend loadConfig
    // fallback for a config without the remoteChat key) to avoid cross-test pollution
    appConfig.remoteChat = { enabled: false, host: '', port: 8080, apiKey: '' }
    // reset sidebar collapsed state to avoid cross-test pollution (readStoredSidebarCollapsed runs at module
    // load time; explicitly reset to default collapsed state so each test starts collapsed)
    appConfig.sidebarCollapsed = true
    appConfig.loaded = false
    locale.value = 'zh'
  })

  it('loadConfig success writes theme, llamaCppDir and modelsDir', async () => {
    mockGetConfig.mockResolvedValue({ theme: 'light', llamaCppDir: 'C:/llama-cpp', modelsDir: 'D:/models', llamaCppDownloadDir: 'E:/llama-dl', modelDownloadDir: 'F:/model-dl', downloadSource: 'modelscope', language: 'auto', resolvedLanguage: 'en', trayEnabled: false })

    await loadConfig()

    expect(appConfig.theme).toBe('light')
    expect(appConfig.llamaCppDir).toBe('C:/llama-cpp')
    expect(appConfig.modelsDir).toBe('D:/models')
    expect(appConfig.llamaCppDownloadDir).toBe('E:/llama-dl')
    expect(appConfig.modelDownloadDir).toBe('F:/model-dl')
    expect(appConfig.downloadSource).toBe('modelscope')
    expect(appConfig.trayEnabled).toBe(false)
    expect(appConfig.loaded).toBe(true)
    expect(localStorage.getItem('llama-desktop-theme')).toBe('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })

  it('loadConfig falls back to empty download dirs when backend omits them', async () => {
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: 'C:/llama-cpp', modelsDir: 'D:/models', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true })

    await loadConfig()

    expect(appConfig.llamaCppDownloadDir).toBe('')
    expect(appConfig.modelDownloadDir).toBe('')
  })

  it('loadConfig reads language/resolvedLanguage and syncs locale', async () => {
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'en', resolvedLanguage: 'en', trayEnabled: true })

    await loadConfig()

    expect(appConfig.language).toBe('en')
    expect(appConfig.resolvedLanguage).toBe('en')
    expect(locale.value).toBe('en')
  })

  it('loadConfig auto resolves zh, locale switches to Chinese', async () => {
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true })

    await loadConfig()

    expect(appConfig.language).toBe('auto')
    expect(appConfig.resolvedLanguage).toBe('zh')
    expect(locale.value).toBe('zh')
  })

  it('loadConfig falls back to empty string when backend omits modelsDir', async () => {
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: 'C:/llama-cpp', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true })

    await loadConfig()

    expect(appConfig.modelsDir).toBe('')
    expect(appConfig.theme).toBe('dark')
  })

  it('loadConfig falls back to hf when backend omits downloadSource', async () => {
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true })

    await loadConfig()

    expect(appConfig.downloadSource).toBe('hf')
  })

  it('loadConfig falls back to true when backend omits trayEnabled', async () => {
    // simulate legacy backend/missing field: when trayEnabled absent, store keeps default true
    // (as any only for constructing missing-field response in tests, differs from backend GetConfig strong typing)
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh' } as any)

    await loadConfig()

    expect(appConfig.trayEnabled).toBe(true)
  })

  it('loadConfig falls back to false when backend omits apiRouteMode, reads explicit true', async () => {
    // legacy backend/missing field: apiRouteMode stays false (GUI never starts in headless mode)
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true } as any)
    await loadConfig()
    expect(appConfig.apiRouteMode).toBe(false)

    // explicit true from the backend is honored
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true, apiRouteMode: true })
    await loadConfig()
    expect(appConfig.apiRouteMode).toBe(true)
  })

  it('loadConfig failure still marks loaded, retains default theme', async () => {
    mockGetConfig.mockRejectedValue(new Error('backend unavailable'))

    await loadConfig()

    expect(appConfig.theme).toBe('light')
    expect(appConfig.loaded).toBe(true)
  })

  it('setTheme updates theme and writes localStorage', async () => {
    mockSetTheme.mockResolvedValue(undefined)

    await setTheme('light')

    expect(appConfig.theme).toBe('light')
    expect(localStorage.getItem('llama-desktop-theme')).toBe('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })

  it('setTheme backend failure degrades silently without throwing', async () => {
    mockSetTheme.mockRejectedValue(new Error('backend unavailable'))

    await expect(setTheme('light')).resolves.toBeUndefined()
    expect(appConfig.theme).toBe('light')
  })

  it('readStoredTheme falls back to old key when new key missing (llama-gui rename migration); light when both absent', () => {
    expect(readStoredTheme()).toBe('light')

    // old installs have only old key: theme preference should continue seamlessly
    localStorage.setItem('llama-gui-theme', 'dark')
    expect(readStoredTheme()).toBe('dark')

    // new key takes priority over old key
    localStorage.setItem('llama-desktop-theme', 'light')
    expect(readStoredTheme()).toBe('light')
  })

  it('setTheme writes only new key, leaves old key (for unupgraded instances to fall back)', async () => {
    mockSetTheme.mockResolvedValue(undefined)
    localStorage.setItem('llama-gui-theme', 'dark')

    await setTheme('dark')

    expect(localStorage.getItem('llama-desktop-theme')).toBe('dark')
    expect(localStorage.getItem('llama-gui-theme')).toBe('dark')
  })

  it('setDownloadSource success updates local state and calls backend', async () => {
    mockSetDownloadSource.mockResolvedValue(undefined)

    await setDownloadSource('modelscope')

    expect(mockSetDownloadSource).toHaveBeenCalledWith('modelscope')
    expect(appConfig.downloadSource).toBe('modelscope')
  })

  it('setDownloadSource backend failure rolls back local state and throws to caller', async () => {
    mockSetDownloadSource.mockRejectedValue(new Error('backend unavailable'))

    await expect(setDownloadSource('modelscope')).rejects.toThrow('backend unavailable')
    expect(appConfig.downloadSource).toBe('hf')
  })

  it('setLanguage success refreshes locale with backend-returned effective language', async () => {
    mockSetLanguage.mockResolvedValue('en')

    await setLanguage('en')

    expect(mockSetLanguage).toHaveBeenCalledWith('en')
    expect(appConfig.language).toBe('en')
    expect(appConfig.resolvedLanguage).toBe('en')
    expect(locale.value).toBe('en')
  })

  it('setLanguage auto refreshes locale per backend detection result', async () => {
    mockSetLanguage.mockResolvedValue('zh')

    await setLanguage('auto')

    expect(appConfig.language).toBe('auto')
    expect(appConfig.resolvedLanguage).toBe('zh')
    expect(locale.value).toBe('zh')
  })

  it('setLanguage backend failure rolls back preference and throws to caller', async () => {
    mockSetLanguage.mockRejectedValue(new Error('backend unavailable'))

    await expect(setLanguage('en')).rejects.toThrow('backend unavailable')
    expect(appConfig.language).toBe('auto')
  })

  it('setTrayEnabled success updates local state and calls backend', async () => {
    mockSetTrayEnabled.mockResolvedValue(undefined)

    await setTrayEnabled(false)

    expect(mockSetTrayEnabled).toHaveBeenCalledWith(false)
    expect(appConfig.trayEnabled).toBe(false)
  })

  it('setTrayEnabled backend failure rolls back local state and throws to caller', async () => {
    mockSetTrayEnabled.mockRejectedValue(new Error('backend unavailable'))

    await expect(setTrayEnabled(false)).rejects.toThrow('backend unavailable')
    expect(appConfig.trayEnabled).toBe(true)
  })

  it('setServerAccessMode success fetches full config, saves with accessMode, updates local state', async () => {
    mockGetServerConfig.mockResolvedValue({ accessMode: 'local', host: '127.0.0.1', apiKey: 'sk-existing', deviceId: 'GPU-test', port: 8080, maxModels: 1, cacheRam: 8192 })
    mockSaveServerConfig.mockResolvedValue(undefined)

    await setServerAccessMode('lan')

    expect(appConfig.serverAccessMode).toBe('lan')
    // saved config is backend latest full config (includes fields user may set elsewhere); only accessMode is overwritten
    expect(mockSaveServerConfig).toHaveBeenCalledWith({ accessMode: 'lan', host: '127.0.0.1', apiKey: 'sk-existing', deviceId: 'GPU-test', port: 8080, maxModels: 1, cacheRam: 8192 })
  })

  it('setServerAccessMode config fetch failure rolls back local state and throws to caller', async () => {
    mockGetServerConfig.mockRejectedValue(new Error('backend unavailable'))

    await expect(setServerAccessMode('lan')).rejects.toThrow('backend unavailable')
    expect(appConfig.serverAccessMode).toBe('local')
    expect(mockSaveServerConfig).not.toHaveBeenCalled()
  })

  it('setServerAccessMode save failure rolls back local state and throws to caller', async () => {
    mockGetServerConfig.mockResolvedValue({ accessMode: 'local', host: '127.0.0.1', apiKey: 'sk-existing', deviceId: '', port: 8080, maxModels: 1, cacheRam: 8192 })
    mockSaveServerConfig.mockRejectedValue(new Error('backend unavailable'))

    await expect(setServerAccessMode('lan')).rejects.toThrow('backend unavailable')
    expect(appConfig.serverAccessMode).toBe('local')
  })

  it('setApiKey success fetches full config, saves with apiKey, updates local state', async () => {
    mockGetServerConfig.mockResolvedValue({ accessMode: 'lan', host: '0.0.0.0', apiKey: 'sk-old', deviceId: 'GPU-old', port: 8080, maxModels: 1, cacheRam: 8192 })
    mockSaveServerConfig.mockResolvedValue(undefined)

    await setApiKey('sk-new')

    expect(appConfig.serverApiKey).toBe('sk-new')
    // saved config is backend latest full config with only apiKey overwritten; other fields (e.g. lan access scope, deviceId) stay intact
    expect(mockSaveServerConfig).toHaveBeenCalledWith({ accessMode: 'lan', host: '0.0.0.0', apiKey: 'sk-new', deviceId: 'GPU-old', port: 8080, maxModels: 1, cacheRam: 8192 })
  })

  it('setApiKey config fetch failure rolls back local state and throws to caller', async () => {
    mockGetServerConfig.mockRejectedValue(new Error('backend unavailable'))

    await expect(setApiKey('sk-new')).rejects.toThrow('backend unavailable')
    expect(appConfig.serverApiKey).toBe('')
    expect(mockSaveServerConfig).not.toHaveBeenCalled()
  })

  it('setApiKey save failure rolls back local state and throws to caller', async () => {
    appConfig.serverApiKey = 'sk-kept'
    mockGetServerConfig.mockResolvedValue({ accessMode: 'local', host: '127.0.0.1', apiKey: 'sk-kept', deviceId: '', port: 8080, maxModels: 1, cacheRam: 8192 })
    mockSaveServerConfig.mockRejectedValue(new Error('backend unavailable'))

    await expect(setApiKey('sk-new')).rejects.toThrow('backend unavailable')
    expect(appConfig.serverApiKey).toBe('sk-kept')
  })

  it('setApiKey accepts an empty value (clearing authentication) and persists it', async () => {
    mockGetServerConfig.mockResolvedValue({ accessMode: 'local', host: '127.0.0.1', apiKey: 'sk-old', deviceId: '', port: 8080, maxModels: 1, cacheRam: 8192 })
    mockSaveServerConfig.mockResolvedValue(undefined)

    await setApiKey('')

    expect(appConfig.serverApiKey).toBe('')
    expect(mockSaveServerConfig).toHaveBeenCalledWith({ accessMode: 'local', host: '127.0.0.1', apiKey: '', deviceId: '', port: 8080, maxModels: 1, cacheRam: 8192 })
  })

  it('loadConfig backend returns sidebarCollapsed: true, state collapses and writes localStorage', async () => {
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true, sidebarCollapsed: true })

    await loadConfig()

    expect(appConfig.sidebarCollapsed).toBe(true)
    expect(localStorage.getItem('llama-desktop-sidebar-collapsed')).toBe('1')
  })

  it('loadConfig backend omits sidebarCollapsed (legacy config), defaults to collapsed', async () => {
    // simulate legacy backend/missing field: when sidebarCollapsed absent, store defaults to true (collapsed,
    // matches backend loadConfig preset default)
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true } as any)

    await loadConfig()

    expect(appConfig.sidebarCollapsed).toBe(true)
    expect(localStorage.getItem('llama-desktop-sidebar-collapsed')).toBe('1')
  })

  it('loadConfig backend returns sidebarCollapsed: false, stays expanded (explicit preference wins)', async () => {
    appConfig.sidebarCollapsed = true // preset default collapsed, verify explicit false (expanded preference) overrides
    mockGetConfig.mockResolvedValue({ theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: '', language: 'auto', resolvedLanguage: 'zh', trayEnabled: true, sidebarCollapsed: false })

    await loadConfig()

    expect(appConfig.sidebarCollapsed).toBe(false)
    expect(localStorage.getItem('llama-desktop-sidebar-collapsed')).toBe('0')
  })

  it('setSidebarCollapsed success updates state, writes localStorage, calls backend', async () => {
    mockSetSidebarCollapsed.mockResolvedValue(undefined)

    await setSidebarCollapsed(true)

    expect(mockSetSidebarCollapsed).toHaveBeenCalledWith(true)
    expect(appConfig.sidebarCollapsed).toBe(true)
    expect(localStorage.getItem('llama-desktop-sidebar-collapsed')).toBe('1')
  })

  it('setSidebarCollapsed backend failure swallows error without rollback (pure UI preference, failure only affects next-launch restore value)', async () => {
    mockSetSidebarCollapsed.mockRejectedValue(new Error('backend unavailable'))

    await expect(setSidebarCollapsed(true)).resolves.toBeUndefined()
    expect(appConfig.sidebarCollapsed).toBe(true)
    expect(localStorage.getItem('llama-desktop-sidebar-collapsed')).toBe('1')
  })

  it('readStoredSidebarCollapsed: no key defaults collapsed, \'1\' collapsed, \'0\' explicitly expanded', () => {
    expect(readStoredSidebarCollapsed()).toBe(true)

    localStorage.setItem('llama-desktop-sidebar-collapsed', '1')
    expect(readStoredSidebarCollapsed()).toBe(true)

    // explicitly write '0' to expand (matches setSidebarCollapsed write-back format)
    localStorage.setItem('llama-desktop-sidebar-collapsed', '0')
    expect(readStoredSidebarCollapsed()).toBe(false)
  })
})

// ─── remoteChat (LAN remote chat pairing) ────────────────────────────────────

describe('store remoteChat', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    localStorage.clear()
    appConfig.remoteChat = { enabled: false, host: '', port: 8080, apiKey: '' }
  })

  it('loadConfig reads the persisted remoteChat pairing from the backend', async () => {
    mockGetConfig.mockResolvedValue({
      theme: 'light', llamaCppDir: '', modelsDir: '', downloadSource: 'hf',
      language: 'auto', resolvedLanguage: 'zh', trayEnabled: true,
      remoteChat: { enabled: true, host: '192.168.1.5', port: 9000, apiKey: 'sk-lan' },
    })

    await loadConfig()

    expect(appConfig.remoteChat).toEqual({ enabled: true, host: '192.168.1.5', port: 9000, apiKey: 'sk-lan' })
  })

  it('loadConfig falls back to documented defaults when backend omits remoteChat (legacy config)', async () => {
    mockGetConfig.mockResolvedValue({
      theme: 'dark', llamaCppDir: '', modelsDir: '', downloadSource: 'hf',
      language: 'auto', resolvedLanguage: 'zh', trayEnabled: true,
    } as any)

    await loadConfig()

    expect(appConfig.remoteChat).toEqual({ enabled: false, host: '', port: 8080, apiKey: '' })
  })

  it('setRemoteChat success updates local state and persists through the backend', async () => {
    mockSaveRemoteChat.mockResolvedValue(undefined)

    const profile = { enabled: true, host: '192.168.1.9', port: 8081, apiKey: 'sk-lan' }
    await setRemoteChat(profile)

    expect(mockSaveRemoteChat).toHaveBeenCalledWith(profile)
    expect(appConfig.remoteChat).toEqual(profile)
  })

  it('setRemoteChat mirrors backend trimming (host/apiKey) into local state on success', async () => {
    mockSaveRemoteChat.mockResolvedValue(undefined)

    await setRemoteChat({ enabled: true, host: '  192.168.1.9  ', port: 8081, apiKey: '  sk-lan  ' })

    expect(appConfig.remoteChat).toEqual({ enabled: true, host: '192.168.1.9', port: 8081, apiKey: 'sk-lan' })
  })

  it('setRemoteChat backend failure rolls back local state and rethrows', async () => {
    const kept = { enabled: true, host: '192.168.1.5', port: 8080, apiKey: 'sk-kept' }
    appConfig.remoteChat = { ...kept }
    mockSaveRemoteChat.mockRejectedValue(new Error('invalid remote port'))

    await expect(setRemoteChat({ enabled: true, host: '192.168.1.9', port: 0, apiKey: '' }))
      .rejects.toThrow('invalid remote port')
    expect(appConfig.remoteChat).toEqual(kept)
  })
})
