import { describe, expect, it } from 'vitest'
import { remoteEndpoint, validateRemoteHost } from '../lib/chatRemote'

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
