import { describe, it, expect } from 'vitest'
import { resolveDocContent, formatDocFetchedAt, DOCS_GITHUB_URLS, docsPageMode, showsSourcePillRow } from '../lib/remoteDocs'
import { buildPlatformState } from '../lib/platform'
import type { RemoteDocResult } from '../wails'

// Build a RemoteDocResult with sensible defaults for one-field variation.
function remote(partial: Partial<RemoteDocResult>): RemoteDocResult {
  return { text: '', source: 'none', fetchedAt: '', ...partial }
}

describe('resolveDocContent', () => {
  it('prefers a usable online remote result', () => {
    const r = resolveDocContent(remote({ text: '# remote', source: 'online', fetchedAt: '2025-08-01T12:00:00Z' }), '# bundled')
    expect(r).toEqual({ text: '# remote', state: 'online' })
  })

  it('maps the backend "cache" source to the "cached" badge state', () => {
    const r = resolveDocContent(remote({ text: '# cached', source: 'cache', fetchedAt: '2025-08-01T12:00:00Z' }), '# bundled')
    expect(r).toEqual({ text: '# cached', state: 'cached' })
  })

  it('falls back to bundled when the remote source is "none"', () => {
    const r = resolveDocContent(remote({ text: '', source: 'none' }), '# bundled')
    expect(r).toEqual({ text: '# bundled', state: 'bundled' })
  })

  it('falls back to bundled when the remote result is null (binding error)', () => {
    const r = resolveDocContent(null, '# bundled')
    expect(r).toEqual({ text: '# bundled', state: 'bundled' })
  })

  it('treats a blank remote text as unusable even with a usable source', () => {
    expect(resolveDocContent(remote({ text: '', source: 'online' }), '# bundled')).toEqual({ text: '# bundled', state: 'bundled' })
    expect(resolveDocContent(remote({ text: '   \n  ', source: 'cache' }), '# bundled')).toEqual({ text: '# bundled', state: 'bundled' })
  })

  it('still lets a usable remote win over a blank bundled text', () => {
    const r = resolveDocContent(remote({ text: '# remote', source: 'online' }), '')
    expect(r).toEqual({ text: '# remote', state: 'online' })
  })
})

describe('formatDocFetchedAt', () => {
  it('returns empty for an empty input', () => {
    expect(formatDocFetchedAt('')).toBe('')
  })

  it('returns empty for an invalid timestamp', () => {
    expect(formatDocFetchedAt('not-a-date')).toBe('')
  })

  it('formats a parseable timestamp as zero-padded local YYYY-MM-DD HH:mm', () => {
    // No timezone offset: ES parses date-time forms without offset as LOCAL
    // time, so the expected output is identical on every machine.
    expect(formatDocFetchedAt('2025-08-01T12:34:56')).toBe('2025-08-01 12:34')
    expect(formatDocFetchedAt('2025-12-05T09:05:00')).toBe('2025-12-05 09:05')
  })

  it('renders offset-carrying timestamps in the fixed shape', () => {
    expect(formatDocFetchedAt('2025-08-01T12:34:56Z')).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
    expect(formatDocFetchedAt('2025-08-01T12:34:56+08:00')).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  })
})

describe('DOCS_GITHUB_URLS', () => {
  it('has a repo docs URL per locale', () => {
    const base = 'https://github.com/CodeNeow/MyLlama/blob/main/frontend/src/docs/'
    expect(DOCS_GITHUB_URLS.zh).toBe(base + 'zh')
    expect(DOCS_GITHUB_URLS.en).toBe(base + 'en')
  })
})

describe('docsPageMode', () => {
  it('classifies the three Docs-page compositions from the tier flags', () => {
    expect(docsPageMode({ isMobile: true, isTablet: false })).toBe('phone')
    expect(docsPageMode({ isMobile: false, isTablet: true })).toBe('tablet')
    expect(docsPageMode({ isMobile: false, isTablet: false })).toBe('desktop')
  })

  it('aligns with the platform tier classifier at the track boundaries', () => {
    // Phone: Android handset (portrait meta case keeps width 390)
    expect(docsPageMode(buildPlatformState('android', 390, 'arm64'))).toBe('phone')
    // Tablet portrait: width band 768..1099 (desktop-OS windows resized into it)
    expect(docsPageMode(buildPlatformState('windows', 800, 'amd64'))).toBe('tablet')
    // Android tablet landscape keeps the wide viewport → desktop tier natively
    expect(docsPageMode(buildPlatformState('android', 1280, 'arm64'))).toBe('desktop')
    // Desktop: wide window stays desktop on every OS
    expect(docsPageMode(buildPlatformState('windows', 1280, 'amd64'))).toBe('desktop')
    // Android width beyond the tablet band is desktop
    expect(docsPageMode(buildPlatformState('android', 1400, 'arm64'))).toBe('desktop')
  })
})

describe('showsSourcePillRow', () => {
  it('shows the pill row on phone and tablet, never on desktop', () => {
    expect(showsSourcePillRow({ isMobile: true, isTablet: false })).toBe(true)
    expect(showsSourcePillRow({ isMobile: false, isTablet: true })).toBe(true)
    expect(showsSourcePillRow({ isMobile: false, isTablet: false })).toBe(false)
  })
})
