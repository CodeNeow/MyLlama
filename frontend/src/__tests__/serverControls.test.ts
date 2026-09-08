import { describe, it, expect, vi, beforeEach } from 'vitest'
import { restartServer } from '../lib/serverControls'
import { startServer, stopServer, getServerStatus } from '../wails'

// mock Wails bridge: bindings are unavailable in the test env (same pattern as store.test.ts)
vi.mock('../wails', () => ({
  getServerStatus: vi.fn(),
  startServer: vi.fn(),
  stopServer: vi.fn(),
}))

const mockStopServer = vi.mocked(stopServer)
const mockStartServer = vi.mocked(startServer)
const mockGetServerStatus = vi.mocked(getServerStatus)

describe('restartServer', () => {
  beforeEach(() => {
    vi.resetAllMocks()
  })

  it('stops, waits until the status reports not running, then starts', async () => {
    // One "still running" report after stop, then the stopped confirmation
    mockGetServerStatus.mockResolvedValueOnce({ running: true, log: [] }).mockResolvedValue({ running: false, log: [] })
    mockStopServer.mockResolvedValue(undefined)
    mockStartServer.mockResolvedValue(undefined)

    await restartServer({ stopTimeoutMs: 2000, pollIntervalMs: 10 })

    expect(mockStopServer).toHaveBeenCalledTimes(1)
    expect(mockGetServerStatus).toHaveBeenCalled()
    expect(mockStartServer).toHaveBeenCalledTimes(1)
    // start must only run after the stopped confirmation landed
    expect(mockStartServer.mock.invocationCallOrder[0]).toBeGreaterThan(
      mockGetServerStatus.mock.invocationCallOrder[0]
    )
  })

  it('starts immediately when the status already reports stopped', async () => {
    mockGetServerStatus.mockResolvedValue({ running: false, log: [] })
    mockStopServer.mockResolvedValue(undefined)
    mockStartServer.mockResolvedValue(undefined)

    await restartServer({ stopTimeoutMs: 2000, pollIntervalMs: 10 })

    expect(mockStartServer).toHaveBeenCalledTimes(1)
  })

  it('proceeds to start when the stopped status never confirms within the bounded wait', async () => {
    mockGetServerStatus.mockResolvedValue({ running: true, log: [] })
    mockStopServer.mockResolvedValue(undefined)
    mockStartServer.mockResolvedValue(undefined)

    await restartServer({ stopTimeoutMs: 60, pollIntervalMs: 10 })

    // The bounded wait lapses but the start half still runs (backend owns the verdict)
    expect(mockGetServerStatus.mock.calls.length).toBeGreaterThan(1)
    expect(mockStartServer).toHaveBeenCalledTimes(1)
  })

  it('tolerates status probe failures while polling', async () => {
    mockGetServerStatus.mockRejectedValueOnce(new Error('boom')).mockResolvedValue({ running: false, log: [] })
    mockStopServer.mockResolvedValue(undefined)
    mockStartServer.mockResolvedValue(undefined)

    await restartServer({ stopTimeoutMs: 2000, pollIntervalMs: 10 })

    expect(mockStartServer).toHaveBeenCalledTimes(1)
  })

  it('rejects and skips the start when the stop binding fails', async () => {
    mockStopServer.mockRejectedValue(new Error('stop failed'))
    mockStartServer.mockResolvedValue(undefined)

    await expect(restartServer({ stopTimeoutMs: 100, pollIntervalMs: 10 })).rejects.toThrow('stop failed')
    expect(mockStartServer).not.toHaveBeenCalled()
  })

  it('rejects when the start binding fails after a confirmed stop', async () => {
    mockGetServerStatus.mockResolvedValue({ running: false, log: [] })
    mockStopServer.mockResolvedValue(undefined)
    mockStartServer.mockRejectedValue(new Error('start failed'))

    await expect(restartServer({ stopTimeoutMs: 100, pollIntervalMs: 10 })).rejects.toThrow('start failed')
    expect(mockStartServer).toHaveBeenCalledTimes(1)
  })
})
