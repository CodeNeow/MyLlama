import { describe, expect, it } from 'vitest'
import {
  clampLoraScale,
  countEnabled,
  enabledLoraRefs,
  mergeLoraRows,
  rowsToRefs,
  type LoraRow,
} from '../lib/lora'
import type { LoraInfo, LoraRef } from '../wails'

// Local fixtures mirroring the wails.ts shapes (structural typing keeps the
// tests decoupled from the generated bindings).
const info = (over: Partial<LoraInfo>): LoraInfo => ({
  name: 'a.gguf',
  path: 'C:\\lora\\a.gguf',
  sizeBytes: 1000,
  sizeHuman: '1.0 KB',
  alpha: 16,
  hasAlpha: true,
  arch: 'llama',
  valid: true,
  ...over,
})

const ref = (over: Partial<LoraRef>): LoraRef => ({
  name: 'a.gguf',
  scale: 1,
  enabled: false,
  ...over,
})

describe('clampLoraScale', () => {
  it('keeps in-range values and trims float noise to 2 decimals', () => {
    expect(clampLoraScale(0.75)).toBe(0.75)
    expect(clampLoraScale(1)).toBe(1)
    expect(clampLoraScale(0.05 * 3)).toBe(0.15)
  })

  it('clamps to the [0, 4] range', () => {
    expect(clampLoraScale(-1)).toBe(0)
    expect(clampLoraScale(9)).toBe(4)
    expect(clampLoraScale(4.5)).toBe(4)
  })

  it('falls back to the llama.cpp default 1.0 for non-finite input', () => {
    expect(clampLoraScale(NaN)).toBe(1)
    expect(clampLoraScale(Infinity)).toBe(1)
  })
})

describe('mergeLoraRows', () => {
  it('overlays persisted refs onto scanned files and defaults new files to unmounted', () => {
    const scan = [
      info({ name: 'a.gguf' }),
      info({ name: 'b.gguf', valid: false, hasAlpha: false, alpha: 0 }),
    ]
    const rows = mergeLoraRows(scan, [ref({ name: 'a.gguf', scale: 0.5, enabled: true })])
    expect(rows).toHaveLength(2)
    expect(rows[0]).toMatchObject({ name: 'a.gguf', scale: 0.5, enabled: true, onDisk: true })
    expect(rows[1]).toMatchObject({ name: 'b.gguf', valid: false, enabled: false, scale: 1 })
  })

  it('keeps persisted refs whose file vanished as onDisk=false rows', () => {
    const rows = mergeLoraRows([info({ name: 'a.gguf' })], [
      ref({ name: 'a.gguf', enabled: true }),
      ref({ name: 'gone.gguf', scale: 2, enabled: true }),
    ])
    expect(rows).toHaveLength(2)
    const missing = rows.find((r) => r.name === 'gone.gguf')
    expect(missing).toMatchObject({ onDisk: false, valid: false, scale: 2 })
  })

  it('handles empty inputs', () => {
    expect(mergeLoraRows([], [])).toEqual([])
    expect(mergeLoraRows([info({ name: 'a.gguf' })], [])).toHaveLength(1)
  })
})

describe('rowsToRefs / enabledLoraRefs / countEnabled', () => {
  const rows: LoraRow[] = [
    {
      name: 'on.gguf',
      sizeHuman: '1 KB',
      alpha: 8,
      hasAlpha: true,
      arch: 'llama',
      valid: true,
      onDisk: true,
      scale: 0.75,
      enabled: true,
    },
    {
      name: 'off.gguf',
      sizeHuman: '1 KB',
      alpha: 8,
      hasAlpha: true,
      arch: 'llama',
      valid: true,
      onDisk: true,
      scale: 2,
      enabled: false,
    },
    {
      name: 'gone.gguf',
      sizeHuman: '—',
      alpha: 0,
      hasAlpha: false,
      arch: '',
      valid: false,
      onDisk: false,
      scale: 1,
      enabled: true,
    },
    {
      name: 'model.gguf',
      sizeHuman: '3 GB',
      alpha: 0,
      hasAlpha: false,
      arch: 'llama',
      valid: false,
      onDisk: true,
      scale: 1,
      enabled: true,
    },
  ]

  it('rowsToRefs keeps every row so toggles survive reloads', () => {
    const refs = rowsToRefs(rows)
    expect(refs).toHaveLength(4)
    expect(refs.every((r) => typeof r.scale === 'number' && typeof r.enabled === 'boolean')).toBe(true)
  })

  it('enabledLoraRefs only mounts enabled + on-disk + valid rows', () => {
    expect(enabledLoraRefs(rows).map((r) => r.name)).toEqual(['on.gguf'])
  })

  it('countEnabled matches the mountable projection', () => {
    expect(countEnabled(rows)).toBe(1)
    expect(countEnabled([])).toBe(0)
  })
})
