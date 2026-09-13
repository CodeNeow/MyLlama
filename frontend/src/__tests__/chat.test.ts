import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  buildChatBody,
  buildMessageContent,
  chatBaseUrl,
  chatContextRows,
  chatContextSummary,
  chatErrorKind,
  chatParamsLayout,
  chatReadiness,
  chatRequestHeaders,
  directModeNeedsSwitch,
  estimateChatTokens,
  estimateConversationTokens,
  fetchRouterModels,
  formatTokenCount,
  isVisionCapable,
  lastReplyTps,
  modelsToUnload,
  parseSSEChunks,
  streamChatCompletion,
  tokenRates,
  unloadRouterModel,
  visionConfigKey,
  type ChatParams,
} from '../lib/chat'

describe('parseSSEChunks', () => {
  // complete single-line JSON
  it('extract single delta', () => {
    const { deltas, rest } = parseSSEChunks('data: {"choices":[{"delta":{"content":"Hello"}}]}\n')
    expect(deltas).toEqual(['Hello'])
    expect(rest).toBe('')
  })

  // multiple data lines arrive at once
  it('multiple data lines arrive at once', () => {
    const buf =
      'data: {"choices":[{"delta":{"content":"Hel"}}]}\n' +
      'data: {"choices":[{"delta":{"content":"lo"}}]}\n' +
      'data: [DONE]\n'
    const { deltas, rest } = parseSSEChunks(buf)
    expect(deltas).toEqual(['Hel', 'lo'])
    expect(rest).toBe('')
  })

  // ignore data: [DONE]
  it('data: [DONE] ignored', () => {
    const { deltas, rest } = parseSSEChunks('data: [DONE]\n')
    expect(deltas).toEqual([])
    expect(rest).toBe('')
  })

  // skip lines with no delta
  it('skip lines with no delta', () => {
    const { deltas, rest } = parseSSEChunks('data: {"choices":[{"delta":{}}]}\n')
    expect(deltas).toEqual([])
    expect(rest).toBe('')
  })

  // ignore non-data lines
  it('ignore non-data lines', () => {
    const { deltas, rest } = parseSSEChunks('keep-alive\ndata: {"choices":[{"delta":{"content":"x"}}]}\n')
    expect(deltas).toEqual(['x'])
    expect(rest).toBe('')
  })

  // split across chunks: JSON truncated between two chunks
  it('split across chunks: JSON truncated between two chunks', () => {
    // first chunk has no newline, entire buffer becomes rest
    const part1 = 'data: {"choices":[{"delta":{"content":"He'
    const { deltas: d1, rest: r1 } = parseSSEChunks(part1)
    expect(d1).toEqual([])
    expect(r1).toBe('data: {"choices":[{"delta":{"content":"He')

    // second chunk continues and includes a newline
    const part2 = 'llo"}}]}\n'
    const { deltas: d2, rest: r2 } = parseSSEChunks(r1 + part2)
    expect(d2).toEqual(['Hello'])
    expect(r2).toBe('')
  })

  // split across chunks: JSON spans two lines and breaks mid-object
  it('split across chunks: JSON spans two lines and breaks mid-object', () => {
    const part1 = 'data: {"choices":[{"delta":{"content":"Hel'
    const { deltas: d1, rest: r1 } = parseSSEChunks(part1)
    expect(d1).toEqual([])
    expect(r1).toBe('data: {"choices":[{"delta":{"content":"Hel')

    const part2 = 'lo"}}]}\n'
    const { deltas: d2, rest: r2 } = parseSSEChunks(r1 + part2)
    expect(d2).toEqual(['Hello'])
    expect(r2).toBe('')
  })

  // trailing fragment preserved
  it('trailing fragment preserved in rest', () => {
    const buf = 'data: {"choices":[{"delta":{"content":"ok"}}]}\ndata: {"cho'
    const { deltas, rest } = parseSSEChunks(buf)
    expect(deltas).toEqual(['ok'])
    expect(rest).toBe('data: {"cho')
  })

  // empty input
  it('empty input returns empty deltas and empty rest', () => {
    const { deltas, reasoningDeltas, rest } = parseSSEChunks('')
    expect(deltas).toEqual([])
    expect(reasoningDeltas).toEqual([])
    expect(rest).toBe('')
  })

  // reasoning-only chunk: goes to reasoningDeltas, not deltas
  it('chunk with only reasoning_content captured in reasoningDeltas, not deltas', () => {
    const { deltas, reasoningDeltas, rest } = parseSSEChunks('data: {"choices":[{"delta":{"reasoning_content":"think"}}]}\n')
    expect(deltas).toEqual([])
    expect(reasoningDeltas).toEqual(['think'])
    expect(rest).toBe('')
  })

  // both fields in one chunk: both captured
  it('chunk with both content and reasoning_content captures both', () => {
    const { deltas, reasoningDeltas } = parseSSEChunks('data: {"choices":[{"delta":{"content":"Hi","reasoning_content":"hm"}}]}\n')
    expect(deltas).toEqual(['Hi'])
    expect(reasoningDeltas).toEqual(['hm'])
  })

  // empty-string reasoning_content is ignored like empty content
  it('empty reasoning_content ignored', () => {
    const { deltas, reasoningDeltas } = parseSSEChunks('data: {"choices":[{"delta":{"content":"Hi","reasoning_content":""}}]}\n')
    expect(deltas).toEqual(['Hi'])
    expect(reasoningDeltas).toEqual([])
  })

  // reasoning JSON truncated between two chunks completes on the next round
  it('split across chunks: reasoning JSON truncated then completed', () => {
    const part1 = 'data: {"choices":[{"delta":{"reasoning_content":"thi'
    const { deltas: d1, reasoningDeltas: r1, rest } = parseSSEChunks(part1)
    expect(d1).toEqual([])
    expect(r1).toEqual([])

    const part2 = 'nk"}}]}\n'
    const { deltas: d2, reasoningDeltas: r2 } = parseSSEChunks(rest + part2)
    expect(d2).toEqual([])
    expect(r2).toEqual(['think'])
  })

  // interleaved content and reasoning lines keep their streams separate
  it('interleaved reasoning and content lines captured separately in order', () => {
    const buf =
      'data: {"choices":[{"delta":{"reasoning_content":"a"}}]}\n' +
      'data: {"choices":[{"delta":{"reasoning_content":"b"}}]}\n' +
      'data: {"choices":[{"delta":{"content":"A"}}]}\n'
    const { deltas, reasoningDeltas } = parseSSEChunks(buf)
    expect(deltas).toEqual(['A'])
    expect(reasoningDeltas).toEqual(['a', 'b'])
  })
})

describe('tokenRates', () => {
  // happy path: both phases defined
  it('computes both phase rates', () => {
    expect(tokenRates(10, 1000, 25, 500)).toEqual({ reasoningTps: 10, answerTps: 50 })
  })

  // zero duration makes the rate undefined (not Infinity)
  it('zero duration yields undefined rates', () => {
    expect(tokenRates(10, 0, 25, 0)).toEqual({})
  })

  // zero tokens makes the rate undefined
  it('zero tokens yields undefined rates', () => {
    expect(tokenRates(0, 1000, 0, 500)).toEqual({})
  })

  // single-sided phases
  it('zero reasoning phase omits reasoningTps only', () => {
    expect(tokenRates(0, 0, 25, 500)).toEqual({ answerTps: 50 })
  })

  it('zero answer phase omits answerTps only', () => {
    expect(tokenRates(10, 1000, 0, 0)).toEqual({ reasoningTps: 10 })
  })

  // raw fractional values: rounding to 1 decimal is a display-layer concern
  it('keeps fractional rates raw without rounding', () => {
    expect(tokenRates(1, 3000, 1, 1500)).toEqual({ reasoningTps: 1 / 3, answerTps: 2 / 3 })
  })
})

describe('chatReadiness', () => {
  // runtime missing takes precedence over a missing model directory
  it('runtime missing yields needRuntime regardless of model count', () => {
    expect(chatReadiness(0, false)).toBe('needRuntime')
    expect(chatReadiness(3, false)).toBe('needRuntime')
  })

  // runtime present but directory empty blocks the start with needModels
  it('runtime ok but no models yields needModels', () => {
    expect(chatReadiness(0, true)).toBe('needModels')
  })

  // both prerequisites met
  it('runtime ok and at least one model yields ok', () => {
    expect(chatReadiness(1, true)).toBe('ok')
    expect(chatReadiness(5, true)).toBe('ok')
  })
})

describe('modelsToUnload', () => {
  // exactly the loaded entries other than the selected id
  it('returns loaded model ids excluding the selected one', () => {
    const loaded = [
      { id: 'a', status: 'loaded' },
      { id: 'b', status: 'loading' },
      { id: 'c', status: 'loaded' },
      { id: 'd', status: 'sleeping' },
    ]
    expect(modelsToUnload(loaded, 'a')).toEqual(['c'])
  })

  // the selected model itself is never unloaded even when loaded
  it('never includes the selected model id', () => {
    expect(modelsToUnload([{ id: 'a', status: 'loaded' }], 'a')).toEqual([])
  })

  // status must be exactly 'loaded': loading/sleeping/failed stay untouched
  it('ignores non-loaded statuses (loading / sleeping / failed)', () => {
    const loaded = [
      { id: 'b', status: 'loading' },
      { id: 'd', status: 'sleeping' },
      { id: 'e', status: 'failed' },
    ]
    expect(modelsToUnload(loaded, 'a')).toEqual([])
  })

  // nothing loaded -> nothing to unload
  it('empty loaded list yields empty result', () => {
    expect(modelsToUnload([], 'a')).toEqual([])
  })
})

describe('directModeNeedsSwitch', () => {
  // the direct-mode server answers exactly the model it hosts
  it('is false while the selected model is the resident one', () => {
    expect(directModeNeedsSwitch(['m1'], 'm1')).toBe(false)
    expect(directModeNeedsSwitch(['m1', 'm2'], 'm2')).toBe(false)
  })

  it('is true when the selected model is not resident (old model would answer)', () => {
    expect(directModeNeedsSwitch(['m1'], 'm2')).toBe(true)
  })

  it('is true with an empty resident list (service up but nothing loaded)', () => {
    expect(directModeNeedsSwitch([], 'm1')).toBe(true)
  })

  it('matches full ids exactly (no prefix/substring or case-insensitive matching)', () => {
    expect(directModeNeedsSwitch(['qwen-7b'], 'qwen-7b-instruct')).toBe(true)
    expect(directModeNeedsSwitch(['Qwen3-8B-GGUF'], 'qwen3-8b-gguf')).toBe(true)
  })
})

describe('buildChatBody', () => {
  it('without params only contains model/messages/stream, no sampling keys', () => {
    const body = buildChatBody('m1', [
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: 'hey' },
    ])
    expect(body).toEqual({
      model: 'm1',
      messages: [
        { role: 'user', content: 'hi' },
        { role: 'assistant', content: 'hey' },
      ],
      stream: true,
    })
  })

  it('with params includes temperature/top_p/top_k/repeat_penalty/max_tokens at top level', () => {
    const params: ChatParams = {
      temperature: 0.7,
      topP: 0.9,
      topK: 20,
      repeatPenalty: 1.05,
      maxTokens: 1024,
      systemPrompt: '',
    }
    const body = buildChatBody('m1', [{ role: 'user', content: 'hi' }], params) as Record<string, unknown>
    expect(body).toMatchObject({
      model: 'm1',
      stream: true,
      temperature: 0.7,
      top_p: 0.9,
      top_k: 20,
      repeat_penalty: 1.05,
      max_tokens: 1024,
    })
  })

  it('when systemPrompt is non-empty, prepend system message to messages', () => {
    const params: ChatParams = {
      temperature: 0.8,
      topP: 0.95,
      topK: 40,
      repeatPenalty: 1.1,
      maxTokens: -1,
      systemPrompt: '你是一个有帮助的助手',
    }
    const body = buildChatBody('m1', [{ role: 'user', content: 'hi' }], params) as Record<string, unknown>
    const msgs = body.messages as Array<{ role: string; content: string }>
    expect(msgs[0]).toEqual({ role: 'system', content: '你是一个有帮助的助手' })
    expect(msgs[1]).toEqual({ role: 'user', content: 'hi' })
  })

  it('when systemPrompt is empty, do not inject system message', () => {
    const params: ChatParams = {
      temperature: 0.8,
      topP: 0.95,
      topK: 40,
      repeatPenalty: 1.1,
      maxTokens: -1,
      systemPrompt: '',
    }
    const body = buildChatBody('m1', [{ role: 'user', content: 'hi' }], params) as Record<string, unknown>
    const msgs = body.messages as Array<{ role: string; content: string }>
    expect(msgs.every(m => m.role !== 'system')).toBe(true)
  })
})

describe('buildMessageContent', () => {
  it('without images returns plain text string', () => {
    expect(buildMessageContent('hello')).toBe('hello')
    expect(buildMessageContent('')).toBe('')
  })

  it('with images returns array with text first, images after', () => {
    const result = buildMessageContent('describe this', ['data:image/png;base64,abc', 'data:image/png;base64,def'])
    expect(result).toEqual([
      { type: 'text', text: 'describe this' },
      { type: 'image_url', image_url: { url: 'data:image/png;base64,abc' } },
      { type: 'image_url', image_url: { url: 'data:image/png;base64,def' } },
    ])
  })

  it('when text is empty and images present, omit text part', () => {
    const result = buildMessageContent('', ['data:image/png;base64,abc'])
    expect(result).toEqual([
      { type: 'image_url', image_url: { url: 'data:image/png;base64,abc' } },
    ])
  })
})

describe('fetchRouterModels', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function jsonResponse(status: number, body: unknown): Response {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(body),
    } as unknown as Response
  }

  it('maps the router /models listing and drops failed entries', async () => {
    const fetchMock = vi.fn((url: string) => {
      expect(url).toBe('http://127.0.0.1:8080/models')
      return Promise.resolve(jsonResponse(200, {
        data: [
          { id: 'm1', status: { value: 'loaded' } },
          { id: 'm2', status: { value: 'failed' } },
        ],
      }))
    })
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchRouterModels({ port: 8080 })).resolves.toEqual([
      { id: 'm1', status: 'loaded' },
    ])
  })

  it('targets the configured LAN host instead of the loopback default', async () => {
    // Remote tier: the endpoint's host replaces 127.0.0.1; port and mapping
    // behavior are unchanged.
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      expect(url).toBe('http://192.168.1.5:9000/models')
      return Promise.resolve(jsonResponse(200, {
        data: [{ id: 'pc-model', status: { value: 'loaded' } }],
      }))
    }))
    await expect(fetchRouterModels({ host: '192.168.1.5', port: 9000 })).resolves.toEqual([
      { id: 'pc-model', status: 'loaded' },
    ])
  })

  it('on 404 falls back to /v1/models and maps ids to loaded', async () => {
    // Direct-mode servers (Android) have no router /models route: the single
    // resident model is listed through the OpenAI-compatible endpoint, where
    // every served model is by definition loaded.
    const fetchMock = vi.fn((url: string) => {
      if (url === 'http://127.0.0.1:8080/models') {
        return Promise.resolve(jsonResponse(404, {}))
      }
      expect(url).toBe('http://127.0.0.1:8080/v1/models')
      return Promise.resolve(jsonResponse(200, {
        data: [{ id: 'resident-a' }, { id: 'resident-b' }],
      }))
    })
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchRouterModels({ port: 8080 })).resolves.toEqual([
      { id: 'resident-a', status: 'loaded' },
      { id: 'resident-b', status: 'loaded' },
    ])
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('maps the native OpenAI /models shape to loaded without /v1/models fallback', async () => {
    // Newer llama.cpp direct-mode builds answer /models natively with the
    // OpenAI shape (data[].id, object:"model", no per-entry status): the
    // entries are resident by definition, so they map to 'loaded'.
    const fetchMock = vi.fn((url: string) => {
      expect(url).toBe('http://127.0.0.1:8080/models')
      return Promise.resolve(jsonResponse(200, {
        object: 'list',
        data: [{ id: 'stories260K', object: 'model', created: 1750000000, owned_by: 'llama.cpp' }],
      }))
    })
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchRouterModels({ port: 8080 })).resolves.toEqual([
      { id: 'stories260K', status: 'loaded' },
    ])
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('returns an empty list when /models answers 200 with no entries', async () => {
    // Both shapes empty (router mode with nothing loaded) must stay empty —
    // no /v1/models fallback, no synthesized loaded entries.
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      expect(url).toBe('http://127.0.0.1:8080/models')
      return Promise.resolve(jsonResponse(200, { object: 'list', data: [] }))
    }))
    await expect(fetchRouterModels({ port: 8080 })).resolves.toEqual([])
  })

  it('keeps non-loaded router statuses as-is instead of remapping them to loaded', async () => {
    // Router-shape entries carry real statuses: pass-through (minus failed).
    // Remapping these to 'loaded' would break unload bookkeeping upstream.
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      expect(url).toBe('http://127.0.0.1:8080/models')
      return Promise.resolve(jsonResponse(200, {
        data: [
          { id: 'm1', status: { value: 'unloaded' } },
          { id: 'm2', status: { value: 'failed' } },
        ],
      }))
    }))
    await expect(fetchRouterModels({ port: 8080 })).resolves.toEqual([
      { id: 'm1', status: 'unloaded' },
    ])
  })

  it('propagates an error when both /models and /v1/models fail', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(jsonResponse(404, {}))))
    await expect(fetchRouterModels({ port: 8080 })).rejects.toThrow('GET /v1/models failed: 404')
  })
})

// ─── Endpoint base URL (LAN remote tier) ─────────────────────────────────────

describe('chatBaseUrl', () => {
  it('defaults an empty or undefined host to the loopback service', () => {
    expect(chatBaseUrl(undefined, 8080)).toBe('http://127.0.0.1:8080')
    expect(chatBaseUrl('', 8080)).toBe('http://127.0.0.1:8080')
  })

  it('joins the configured LAN host and port without a scheme or path', () => {
    expect(chatBaseUrl('192.168.1.5', 9000)).toBe('http://192.168.1.5:9000')
    expect(chatBaseUrl('my-pc.local', 8081)).toBe('http://my-pc.local:8081')
  })
})

// ─── API key auth headers (issue #29: 401 with a key set) ────────────────────

describe('chatRequestHeaders', () => {
  it('adds the Authorization bearer header only for a non-empty key', () => {
    expect(chatRequestHeaders(undefined)).toEqual({})
    expect(chatRequestHeaders('')).toEqual({})
    expect(chatRequestHeaders('test-key-fixture')).toEqual({ Authorization: 'Bearer test-key-fixture' })
  })
})

describe('fetchRouterModels auth', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function jsonResponse(status: number, body: unknown): Response {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(body),
    } as unknown as Response
  }

  it('sends the bearer header when a key is configured', async () => {
    let captured: RequestInit | undefined
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      captured = init
      return Promise.resolve(jsonResponse(200, { data: [{ id: 'm1', status: { value: 'loaded' } }] }))
    }))
    await fetchRouterModels({ port: 8080, apiKey: 'test-key-fixture' })
    expect(captured?.headers).toEqual({ Authorization: 'Bearer test-key-fixture' })
  })

  it('sends no auth header without a key', async () => {
    let captured: RequestInit | undefined
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      captured = init
      return Promise.resolve(jsonResponse(200, { data: [{ id: 'm1', status: { value: 'loaded' } }] }))
    }))
    await fetchRouterModels({ port: 8080 })
    expect(captured?.headers).toEqual({})
  })
})

describe('streamChatCompletion auth', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function sseResponse(): Response {
    return {
      ok: true,
      status: 200,
      body: {
        getReader: () => ({
          read: () => Promise.resolve({ done: true, value: undefined }),
          releaseLock: () => {},
        }),
      },
    } as unknown as Response
  }

  it('carries the bearer header alongside Content-Type when a key is configured', async () => {
    let captured: RequestInit | undefined
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      captured = init
      expect(url).toBe('http://127.0.0.1:8080/v1/chat/completions')
      return Promise.resolve(sseResponse())
    }))
    const noop = () => {}
    const controller = new AbortController()
    await streamChatCompletion(
      { port: 8080, apiKey: 'test-key-fixture' },
      'm1',
      [{ role: 'user', content: 'hi' }],
      noop,
      noop,
      controller.signal
    )
    expect(captured?.headers).toEqual({
      'Content-Type': 'application/json',
      Authorization: 'Bearer test-key-fixture',
    })
  })

  it('keeps the headers unchanged without a key', async () => {
    let captured: RequestInit | undefined
    vi.stubGlobal('fetch', vi.fn((url: string, init?: RequestInit) => {
      captured = init
      return Promise.resolve(sseResponse())
    }))
    const noop = () => {}
    const controller = new AbortController()
    await streamChatCompletion({ port: 8080 }, 'm1', [{ role: 'user', content: 'hi' }], noop, noop, controller.signal)
    expect(captured?.headers).toEqual({ 'Content-Type': 'application/json' })
  })

  it('targets the endpoint host for the streaming request', async () => {
    vi.stubGlobal('fetch', vi.fn((url: string) => {
      expect(url).toBe('http://192.168.1.5:9000/v1/chat/completions')
      return Promise.resolve(sseResponse())
    }))
    const noop = () => {}
    const controller = new AbortController()
    await streamChatCompletion(
      { host: '192.168.1.5', port: 9000 },
      'm1',
      [{ role: 'user', content: 'hi' }],
      noop,
      noop,
      controller.signal
    )
  })
})

// ─── unloadRouterModel (single unload failure must not block the chat) ──────

describe('unloadRouterModel', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function jsonResponse(status: number, body: unknown): Response {
    return {
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(body),
    } as unknown as Response
  }

  it('posts {"model": id} to /models/unload and resolves true on success:true', async () => {
    let capturedUrl = ''
    let capturedInit: RequestInit | undefined
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      capturedUrl = url
      capturedInit = init
      return jsonResponse(200, { success: true })
    }))
    await expect(unloadRouterModel({ host: '192.168.1.5', port: 9000 }, 'other-model')).resolves.toBe(true)
    expect(capturedUrl).toBe('http://192.168.1.5:9000/models/unload')
    expect(capturedInit?.method).toBe('POST')
    expect(capturedInit?.body).toBe('{"model":"other-model"}')
    expect((capturedInit?.headers as Record<string, string>)['Content-Type']).toBe('application/json')
  })

  it('resolves false on a non-2xx response', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(400, { error: { message: 'model is not running' } })))
    await expect(unloadRouterModel({ port: 8080 }, 'gone-model')).resolves.toBe(false)
  })

  it('resolves false when the 2xx body reports success=false', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(200, { success: false })))
    await expect(unloadRouterModel({ port: 8080 }, 'm1')).resolves.toBe(false)
  })

  it('resolves false on a network-level failure instead of throwing', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    }))
    await expect(unloadRouterModel({ port: 8080 }, 'm1')).resolves.toBe(false)
  })

  it('resolves false when the 2xx body is not JSON', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      json: () => Promise.reject(new Error('bad json')),
    } as unknown as Response)))
    await expect(unloadRouterModel({ port: 8080 }, 'm1')).resolves.toBe(false)
  })
})

// ─── Chat layout helpers (tablet design draft frames ⑤⑥⑦) ──────────────────

describe('chatParamsLayout', () => {
  it('phone tier keeps the docked bottom sheet', () => {
    expect(chatParamsLayout(true, false)).toBe('sheet')
  })

  it('tablet portrait gets the centered modal card, not the phone sheet', () => {
    expect(chatParamsLayout(false, true)).toBe('modal')
  })

  it('desktop keeps the anchored popover', () => {
    expect(chatParamsLayout(false, false)).toBe('popover')
  })

  it('phone check wins over the tablet check (defensive ordering)', () => {
    expect(chatParamsLayout(true, true)).toBe('sheet')
  })
})

describe('estimateChatTokens', () => {
  it('counts ~4 latin characters per token, rounded up', () => {
    expect(estimateChatTokens('Hello, world!')).toBe(4)
    expect(estimateChatTokens('ab')).toBe(1)
  })

  it('counts CJK characters at 0.75 tokens each, rounded up', () => {
    expect(estimateChatTokens('你好世界')).toBe(3)
  })

  it('handles mixed scripts', () => {
    // 2 CJK * 0.75 + 6 latin / 4 = 1.5 + 1.5 = 3
    expect(estimateChatTokens('你好 world')).toBe(3)
  })

  it('empty text estimates zero tokens', () => {
    expect(estimateChatTokens('')).toBe(0)
  })
})

describe('estimateConversationTokens', () => {
  it('sums the per-message estimates', () => {
    const msgs = [
      { role: 'user', content: '你好' },
      { role: 'assistant', content: 'Hi there' },
    ]
    // 2 * 0.75 -> 2 ; 8 / 4 -> 2
    expect(estimateConversationTokens(msgs)).toBe(4)
  })

  it('empty history estimates zero', () => {
    expect(estimateConversationTokens([])).toBe(0)
  })
})

describe('lastReplyTps', () => {
  it('returns the most recent assistant answer rate', () => {
    const msgs = [
      { role: 'user', content: 'a' },
      { role: 'assistant', content: 'b', stats: { answerTps: 31.8 } },
      { role: 'user', content: 'c' },
      { role: 'assistant', content: 'd', stats: { reasoningTps: 14.6, answerTps: 40 } },
    ]
    expect(lastReplyTps(msgs)).toBe(40)
  })

  it('falls back to the reasoning rate when no answer rate exists', () => {
    expect(lastReplyTps([{ role: 'assistant', stats: { reasoningTps: 14.6 } }])).toBe(14.6)
  })

  it('skips user messages and stats-less assistant messages', () => {
    expect(lastReplyTps([{ role: 'assistant' }])).toBeNull()
    expect(lastReplyTps([])).toBeNull()
  })
})

describe('formatTokenCount', () => {
  it('renders small counts as integers', () => {
    expect(formatTokenCount(0)).toBe('0')
    expect(formatTokenCount(832)).toBe('832')
  })

  it('renders thousands with one decimal + k suffix', () => {
    expect(formatTokenCount(1234)).toBe('1.2k')
    expect(formatTokenCount(1000)).toBe('1.0k')
  })

  it('degrades non-finite and negative inputs to zero', () => {
    expect(formatTokenCount(-5)).toBe('0')
    expect(formatTokenCount(Number.NaN)).toBe('0')
  })
})

describe('chatContextSummary', () => {
  const base = {
    serverRunning: true,
    starting: false,
    blocked: false,
    streaming: false,
    modelLabel: 'Qwen3-4B',
    liveTps: null,
    messages: [{ role: 'user', content: 'hi' }, { role: 'assistant', content: 'hello', stats: { answerTps: 31.2 } }],
  }

  it('reports running service, last-reply speed and token estimate', () => {
    const s = chatContextSummary(base)
    expect(s.service).toBe('running')
    expect(s.speedTps).toBe(31.2)
    expect(s.messageCount).toBe(2)
    expect(s.estimatedTokens).toBeGreaterThan(0)
    expect(s.suggestRestart).toBe(false)
    expect(s.showContextCard).toBe(true)
  })

  it('prefers the live streaming rate over the last reply rate', () => {
    expect(chatContextSummary({ ...base, liveTps: 55.5 }).speedTps).toBe(55.5)
  })

  it('marks the service down with the restart suggestion when blocked', () => {
    const s = chatContextSummary({ ...base, serverRunning: false, blocked: true })
    expect(s.service).toBe('down')
    expect(s.suggestRestart).toBe(true)
  })

  it('no suggestion while the bring-up is in progress', () => {
    const s = chatContextSummary({ ...base, serverRunning: false, starting: true, blocked: true })
    expect(s.service).toBe('starting')
    expect(s.suggestRestart).toBe(false)
  })

  it('hides the context card for an idle empty chat (params-only rail, draft B5)', () => {
    const s = chatContextSummary({
      serverRunning: false,
      starting: false,
      blocked: false,
      streaming: false,
      modelLabel: '',
      liveTps: null,
      messages: [],
    })
    expect(s.showContextCard).toBe(false)
    expect(s.speedTps).toBeNull()
  })

  it('shows the context card while streaming even with empty history', () => {
    const s = chatContextSummary({ ...base, messages: [], streaming: true })
    expect(s.showContextCard).toBe(true)
  })
})

describe('chatContextRows', () => {
  const copy = {
    serviceRunning: 'Running',
    serviceStarting: 'Starting…',
    serviceDown: 'Not running',
    noModel: 'No models',
    noSpeed: '—',
    formatTps: (tps: number) => `${tps.toFixed(1)} tok/s`,
    suggestion: 'Start the service and retry',
  }

  it('renders service / model / speed rows for a healthy session', () => {
    const rows = chatContextRows(
      chatContextSummary({
        serverRunning: true,
        starting: false,
        blocked: false,
        streaming: false,
        modelLabel: 'Qwen3-4B',
        liveTps: null,
        messages: [{ role: 'assistant', content: 'x', stats: { answerTps: 31.2 } }],
      }),
      copy
    )
    expect(rows).toEqual([
      { key: 'service', value: 'Running', tone: 'ok' },
      { key: 'model', value: 'Qwen3-4B', tone: 'plain' },
      { key: 'speed', value: '31.2 tok/s', tone: 'plain' },
    ])
  })

  it('renders the down state + suggestion rows when blocked (draft B7)', () => {
    const rows = chatContextRows(
      chatContextSummary({
        serverRunning: false,
        starting: false,
        blocked: true,
        streaming: false,
        modelLabel: 'Qwen3-4B',
        liveTps: null,
        messages: [],
      }),
      copy
    )
    expect(rows).toEqual([
      { key: 'service', value: 'Not running', tone: 'error' },
      { key: 'model', value: 'Qwen3-4B', tone: 'plain' },
      { key: 'speed', value: '—', tone: 'plain' },
      { key: 'suggestion', value: 'Start the service and retry', tone: 'error' },
    ])
  })

  it('uses the no-model placeholder label when nothing is picked', () => {
    const rows = chatContextRows(
      chatContextSummary({
        serverRunning: true,
        starting: false,
        blocked: false,
        streaming: false,
        modelLabel: '',
        liveTps: null,
        messages: [],
      }),
      copy
    )
    expect(rows.find((r) => r.key === 'model')?.value).toBe('No models')
  })
})

describe('isVisionCapable', () => {
  // Vision gate (issue #35): scan-found mmproj OR an explicit projector path
  // from the model settings counts as vision capability
  it('scan-found mmproj is capable regardless of the override', () => {
    expect(isVisionCapable(true, undefined)).toBe(true)
    expect(isVisionCapable(true, '')).toBe(true)
    expect(isVisionCapable(true, 'mmproj-f16.gguf')).toBe(true)
  })

  it('no scanned mmproj needs a non-blank explicit override', () => {
    expect(isVisionCapable(false, undefined)).toBe(false)
    expect(isVisionCapable(false, null)).toBe(false)
    expect(isVisionCapable(false, '')).toBe(false)
    expect(isVisionCapable(false, '   ')).toBe(false)
    expect(isVisionCapable(false, 'C:/models/mmproj-f16.gguf')).toBe(true)
  })
})

describe('visionConfigKey', () => {
  // Per-model configs are keyed by display Name on the backend, while the
  // chat picker carries the router id (alias || name) — the key must resolve
  // back to the Name or the explicit mmproj override lookup misses
  const models = [
    { name: 'Qwen3-VL-8B', alias: 'Qwen3-VL-8B-2' },
    { name: 'Llama-3.2-3B' },
  ]

  it('resolves an aliased router id to its display Name', () => {
    expect(visionConfigKey(models, 'Qwen3-VL-8B-2')).toBe('Qwen3-VL-8B')
  })

  it('resolves a plain-name id to itself', () => {
    expect(visionConfigKey(models, 'Llama-3.2-3B')).toBe('Llama-3.2-3B')
  })

  it('falls back to the selected id when no record matches', () => {
    expect(visionConfigKey(models, 'Gone-Model')).toBe('Gone-Model')
  })

  it('empty list falls back to the selected id', () => {
    expect(visionConfigKey([], 'Llama-3.2-3B')).toBe('Llama-3.2-3B')
  })
})

describe('chatErrorKind', () => {
  // llama-server's raw image-input rejection maps to guided copy (issue #35)
  it('matches the known image-unsupported and mmproj errors', () => {
    expect(chatErrorKind('image input is not supported - hint: the model may not support it')).toBe('vision-unsupported')
    expect(chatErrorKind("this model does not support mmproj - make sure it isn't corrupt")).toBe('vision-unsupported')
  })

  it('matches case-insensitively', () => {
    expect(chatErrorKind('Image Input Is Not Supported By This Model')).toBe('vision-unsupported')
    expect(chatErrorKind('MMPROJ file missing')).toBe('vision-unsupported')
  })

  it('returns null for unrelated errors', () => {
    expect(chatErrorKind('connection refused')).toBeNull()
    expect(chatErrorKind('HTTP 500')).toBeNull()
    expect(chatErrorKind('')).toBeNull()
  })
})
