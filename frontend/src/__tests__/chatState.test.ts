import { describe, it, expect, vi, beforeEach } from 'vitest'
import { capMessages, ChatMessage, loadChatHistory, persistChat, reconcileSelectedModel, messages, selectedModel } from '../lib/chatState'

describe('capMessages', () => {
  it('truncate keeping most recent N entries', () => {
    const msgs: ChatMessage[] = Array.from({ length: 250 }, (_, i) => ({ role: 'user' as const, content: String(i) }))
    const capped = capMessages(msgs, 200)
    expect(capped).toHaveLength(200)
    expect(capped[0].content).toBe('50')
    expect(capped[199].content).toBe('249')
  })

  it('under limit returns as-is', () => {
    const msgs: ChatMessage[] = [{ role: 'user', content: 'a' }]
    expect(capMessages(msgs, 200)).toBe(msgs)
  })

  it('empty array returns empty array', () => {
    expect(capMessages([], 200)).toEqual([])
  })
})

describe('reconcileSelectedModel', () => {
  it('stored model still available is kept unchanged', () => {
    expect(reconcileSelectedModel('m1', ['m1', 'm2'])).toEqual({ model: 'm1', changed: false, action: 'kept' })
  })

  it('stale stored model falls back to the first available model', () => {
    expect(reconcileSelectedModel('gone', ['m1', 'm2'])).toEqual({ model: 'm1', changed: true, action: 'switched' })
  })

  it('empty available list clears the stored selection (guided re-pick)', () => {
    expect(reconcileSelectedModel('gone', [])).toEqual({ model: '', changed: true, action: 'cleared' })
  })

  it('empty available list with an empty stored model reports cleared without a change', () => {
    expect(reconcileSelectedModel('', [])).toEqual({ model: '', changed: false, action: 'cleared' })
  })

  it('empty stored model with a non-empty list picks the first model', () => {
    expect(reconcileSelectedModel('', ['m1'])).toEqual({ model: 'm1', changed: true, action: 'switched' })
  })
})

describe('chatState module-level state and persistence', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  it('loadChatHistory restores valid JSON message list', async () => {
    const saved: ChatMessage[] = [
      { role: 'user', content: 'hello' },
      { role: 'assistant', content: 'world' },
    ]
    localStorage.setItem('llama-desktop-chat-messages', JSON.stringify(saved))

    const { loadChatHistory: lch, messages: msgs } = await import('../lib/chatState')
    lch()

    expect(msgs.value).toHaveLength(2)
    expect(msgs.value[0]).toEqual({ role: 'user', content: 'hello' })
  })

  it('loadChatHistory invalid JSON silently falls back to empty array', async () => {
    localStorage.setItem('llama-desktop-chat-messages', 'not-json')

    const { loadChatHistory: lch, messages: msgs } = await import('../lib/chatState')
    lch()

    expect(msgs.value).toEqual([])
  })

  it('loadChatHistory over-cap data auto-truncates to cap', async () => {
    const saved: ChatMessage[] = Array.from({ length: 250 }, (_, i) => ({ role: 'user' as const, content: String(i) }))
    localStorage.setItem('llama-desktop-chat-messages', JSON.stringify(saved))

    const { loadChatHistory: lch, messages: msgs } = await import('../lib/chatState')
    lch()

    expect(msgs.value).toHaveLength(200)
    expect(msgs.value[0].content).toBe('50')
  })

  it('loadChatHistory restores selectedModel', async () => {
    localStorage.setItem('llama-desktop-chat-model', 'llama-2-7b-chat')

    const { loadChatHistory: lch, selectedModel: model } = await import('../lib/chatState')
    lch()

    expect(model.value).toBe('llama-2-7b-chat')
  })

  it('loadChatHistory without MODEL_KEY keeps selectedModel empty string', async () => {
    const { loadChatHistory: lch, selectedModel: model } = await import('../lib/chatState')
    lch()

    expect(model.value).toBe('')
  })

  it('persistChat writes messages and selectedModel to localStorage', async () => {
    const { messages: msgs, selectedModel: model, persistChat, loadChatHistory: lch } = await import('../lib/chatState')
    msgs.value = [{ role: 'user', content: 'hi' }]
    model.value = 'test-model'
    persistChat()

    // verify persistence via fresh module instance
    vi.resetModules()
    const { messages: msgs2, selectedModel: model2, loadChatHistory: lch2 } = await import('../lib/chatState')
    lch2()

    expect(msgs2.value).toEqual([{ role: 'user', content: 'hi' }])
    expect(model2.value).toBe('test-model')
  })

  it('persistChat empty selectedModel removes MODEL_KEY', async () => {
    const { selectedModel: model, persistChat } = await import('../lib/chatState')
    model.value = ''
    persistChat()

    expect(localStorage.getItem('llama-desktop-chat-model')).toBeNull()
  })

  it('persistChat strips images but keeps reasoning and stats; load restores them', async () => {
    const { messages: msgs, persistChat } = await import('../lib/chatState')
    msgs.value = [
      {
        role: 'assistant',
        content: 'hello',
        images: ['data:image/png;base64,abc'],
        reasoning: 'thinking...',
        stats: { reasoningTps: 12.5, answerTps: 30.2 },
      },
    ]
    persistChat()

    // persisted JSON must not contain the image payload but must keep reasoning/stats
    const raw = JSON.parse(localStorage.getItem('llama-desktop-chat-messages') || '[]')
    expect(raw).toEqual([
      { role: 'assistant', content: 'hello', reasoning: 'thinking...', stats: { reasoningTps: 12.5, answerTps: 30.2 } },
    ])

    // fresh module instance restores reasoning and stats
    vi.resetModules()
    const { messages: msgs2, loadChatHistory: lch2 } = await import('../lib/chatState')
    lch2()
    expect(msgs2.value[0].reasoning).toBe('thinking...')
    expect(msgs2.value[0].stats).toEqual({ reasoningTps: 12.5, answerTps: 30.2 })
  })

  it('module singleton isolation: vi.resetModules + dynamic import gives fresh state', async () => {
    // first import and mutate state
    const { messages: msgs1, persistChat: p1 } = await import('../lib/chatState')
    msgs1.value = [{ role: 'user', content: 'first' }]
    p1()

    // clear localStorage then reset modules and re-import, verify fresh state (loadChatHistory reads empty)
    localStorage.clear()
    vi.resetModules()
    const { messages: msgs2, loadChatHistory: lch2 } = await import('../lib/chatState')
    lch2()

    expect(msgs2.value).toEqual([])
    // confirm it is a new ref object
    expect(msgs2).not.toBe(msgs1)
  })
})
