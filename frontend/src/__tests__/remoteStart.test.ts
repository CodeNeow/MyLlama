import { afterEach, describe, expect, it, vi } from 'vitest'
import { REMOTE_START_PORT, remoteStartService } from '../lib/remoteStart'

// remoteStartService (Phase R): the phone-side POST /start against the PC's
// control plane — status-code-to-result mapping, request shape (fixed port
// 1900, POST, X-Control-Token), the 5s self-abort when no signal is passed,
// and caller-signal passthrough.

describe('remoteStartService', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('posts to the control plane on port 1900 with the key as X-Control-Token', async () => {
    let capturedUrl = ''
    let capturedInit: RequestInit | undefined
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      capturedUrl = url
      capturedInit = init
      return { status: 200 }
    }))
    await expect(remoteStartService('192.168.1.5', 'pair-key')).resolves.toBe('started')
    expect(capturedUrl).toBe(`http://192.168.1.5:${REMOTE_START_PORT}/start`)
    expect(capturedInit?.method).toBe('POST')
    expect(capturedInit?.headers).toEqual({ 'X-Control-Token': 'pair-key' })
  })

  it('maps status codes to results (200/409/401/403/500)', async () => {
    const cases: Array<{ status: number; want: string }> = [
      { status: 200, want: 'started' },
      { status: 409, want: 'already-running' },
      { status: 401, want: 'not-allowed' },
      { status: 403, want: 'not-allowed' },
      { status: 500, want: 'failed' },
      { status: 404, want: 'failed' },
    ]
    for (const c of cases) {
      vi.stubGlobal('fetch', vi.fn(async () => ({ status: c.status })))
      await expect(remoteStartService('my-pc', 'k')).resolves.toBe(c.want)
    }
  })

  it('maps network-layer failures to unreachable (refused / DNS unified)', async () => {
    // Connection refused / unreachable host surface as TypeError in fetch
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    }))
    await expect(remoteStartService('192.168.1.5', 'k')).resolves.toBe('unreachable')
  })

  it('passes the caller signal to fetch instead of the internal timeout controller', async () => {
    let capturedInit: RequestInit | undefined
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      capturedInit = init
      return { status: 200 }
    }))
    const controller = new AbortController()
    await expect(remoteStartService('pc', 'k', controller.signal)).resolves.toBe('started')
    expect(capturedInit?.signal).toBe(controller.signal)
  })

  it('aborts on its own 5s deadline when no caller signal is given', async () => {
    vi.useFakeTimers()
    let aborted = false
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init?: RequestInit) => {
      return new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => {
          aborted = true
          reject(new DOMException('aborted', 'AbortError'))
        })
      })
    }))
    const pending = remoteStartService('192.168.1.5', 'k')
    await vi.advanceTimersByTimeAsync(4999)
    expect(aborted).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    await expect(pending).resolves.toBe('unreachable')
  })

  it('returns failed without calling fetch when the host is blank', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(remoteStartService('   ', 'k')).resolves.toBe('failed')
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
