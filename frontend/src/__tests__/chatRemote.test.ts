import { describe, expect, it } from 'vitest'
import { buildPairPayload, parsePairPayload, remoteEndpoint, validateRemoteHost } from '../lib/chatRemote'

describe('validateRemoteHost', () => {
  // usable host: the bare host/IP part of host[:port] — null means valid
  it('accepts a plain IPv4 or hostname (null = valid)', () => {
    expect(validateRemoteHost('192.168.1.5')).toBeNull()
    expect(validateRemoteHost('my-pc.local')).toBeNull()
  })

  it('accepts a host padded with whitespace (trimmed before checks)', () => {
    expect(validateRemoteHost('  192.168.1.5  ')).toBeNull()
  })

  it('rejects an empty or whitespace-only host with errHostEmpty', () => {
    expect(validateRemoteHost('')).toBe('settings.remoteChat.errHostEmpty')
    expect(validateRemoteHost('   ')).toBe('settings.remoteChat.errHostEmpty')
  })

  it('rejects a scheme-bearing host with errHostScheme', () => {
    expect(validateRemoteHost('http://192.168.1.5')).toBe('settings.remoteChat.errHostScheme')
    expect(validateRemoteHost('https://my-pc.local')).toBe('settings.remoteChat.errHostScheme')
  })

  it('rejects a path-bearing host with errHostScheme', () => {
    expect(validateRemoteHost('192.168.1.5/models')).toBe('settings.remoteChat.errHostScheme')
    expect(validateRemoteHost('my-pc.local/x')).toBe('settings.remoteChat.errHostScheme')
  })
})

describe('remoteEndpoint', () => {
  const profile = { enabled: true, host: '192.168.1.5', port: 8080, apiKey: 'sk-lan' }

  it('maps an enabled profile with a host to the direct endpoint', () => {
    expect(remoteEndpoint(profile)).toEqual({ host: '192.168.1.5', port: 8080, apiKey: 'sk-lan' })
  })

  it('returns null when the profile is disabled (local tier fallback)', () => {
    expect(remoteEndpoint({ ...profile, enabled: false })).toBeNull()
  })

  it('returns null when the host is empty even while enabled', () => {
    expect(remoteEndpoint({ ...profile, host: '' })).toBeNull()
  })

  it('passes the empty key through (no auth header sent downstream)', () => {
    expect(remoteEndpoint({ ...profile, apiKey: '' })).toEqual({ host: '192.168.1.5', port: 8080, apiKey: '' })
  })
})

describe('buildPairPayload', () => {
  it('builds the canonical v=1 payload with host, port and key segments', () => {
    expect(buildPairPayload({ host: '192.168.1.5', port: 8080, apiKey: 'sk-lan' }))
      .toBe('myllama://pair?v=1&host=192.168.1.5&port=8080&key=sk-lan')
  })

  it('omits the key segment when the key is empty', () => {
    expect(buildPairPayload({ host: 'my-pc.local', port: 8080, apiKey: '' }))
      .toBe('myllama://pair?v=1&host=my-pc.local&port=8080')
  })

  it('percent-encodes special characters in the key and round trips exactly', () => {
    const built = buildPairPayload({ host: 'my-pc.local', port: 80, apiKey: 'a&b=c d+e' })
    // The raw "&" must be encoded or the payload would split into two params
    expect(built).not.toContain('&b=')
    expect(parsePairPayload(built)).toEqual({ host: 'my-pc.local', port: 80, apiKey: 'a&b=c d+e' })
  })

  it('returns the empty string for an empty host so callers hide the QR', () => {
    expect(buildPairPayload({ host: '', port: 8080, apiKey: '' })).toBe('')
    expect(buildPairPayload({ host: '   ', port: 8080, apiKey: '' })).toBe('')
  })

  it('returns the empty string for an out-of-range port', () => {
    expect(buildPairPayload({ host: '192.168.1.5', port: 0, apiKey: '' })).toBe('')
    expect(buildPairPayload({ host: '192.168.1.5', port: 65536, apiKey: '' })).toBe('')
  })
})

describe('parsePairPayload', () => {
  it('parses a payload built by buildPairPayload (round trip)', () => {
    expect(parsePairPayload('myllama://pair?v=1&host=192.168.1.5&port=8080&key=sk-lan'))
      .toEqual({ host: '192.168.1.5', port: 8080, apiKey: 'sk-lan' })
  })

  it('defaults the key to the empty string when the segment is missing', () => {
    expect(parsePairPayload('myllama://pair?v=1&host=pc.local&port=80'))
      .toEqual({ host: 'pc.local', port: 80, apiKey: '' })
  })

  it('accepts surrounding whitespace (QR scanners may append newlines)', () => {
    expect(parsePairPayload('  myllama://pair?v=1&host=1.2.3.4&port=1\n'))
      .toEqual({ host: '1.2.3.4', port: 1, apiKey: '' })
  })

  it('returns null for arbitrary URLs, JSON and plain text', () => {
    expect(parsePairPayload('https://example.com/pair?v=1&host=x&port=80')).toBeNull()
    expect(parsePairPayload('myllama://other?v=1&host=x&port=80')).toBeNull()
    expect(parsePairPayload('{"host":"192.168.1.5","port":8080}')).toBeNull()
    expect(parsePairPayload('192.168.1.5')).toBeNull()
    expect(parsePairPayload('')).toBeNull()
  })

  it('returns null for a wrong or missing version', () => {
    expect(parsePairPayload('myllama://pair?v=2&host=x&port=80')).toBeNull()
    expect(parsePairPayload('myllama://pair?host=x&port=80')).toBeNull()
  })

  it('returns null for a bad port', () => {
    expect(parsePairPayload('myllama://pair?v=1&host=x&port=0')).toBeNull()
    expect(parsePairPayload('myllama://pair?v=1&host=x&port=65536')).toBeNull()
    expect(parsePairPayload('myllama://pair?v=1&host=x&port=abc')).toBeNull()
    expect(parsePairPayload('myllama://pair?v=1&host=x')).toBeNull()
  })

  it('returns null for an empty, scheme-bearing or path-bearing host', () => {
    expect(parsePairPayload('myllama://pair?v=1&host=&port=80')).toBeNull()
    expect(parsePairPayload('myllama://pair?v=1&host=http://1.2.3.4&port=80')).toBeNull()
    expect(parsePairPayload('myllama://pair?v=1&host=1.2.3.4/models&port=80')).toBeNull()
  })
})
