import { describe, it, expect } from 'vitest'
import { sortModelFiles, guessQuant, matchLoadedModelSize, selectedBytes, localLibraryStats, isMMProjFile } from '../lib/modelFiles'

describe('sortModelFiles', () => {
  // sort by size descending; missing size defaults to 0 (sorted to end)
  it('sort by size descending', () => {
    const files = [
      { filename: 'a.bin', size: 100 },
      { filename: 'b.bin', size: 500 },
      { filename: 'c.bin', size: 200 },
    ]
    const result = sortModelFiles(files)
    expect(result.map(f => f.filename)).toEqual(['b.bin', 'c.bin', 'a.bin'])
  })

  it('missing size defaults to 0 and sorts to end', () => {
    const files = [
      { filename: 'a.bin', size: 100 },
      { filename: 'b.bin' },
      { filename: 'c.bin' },
    ]
    const result = sortModelFiles(files)
    expect(result.map(f => f.filename)).toEqual(['a.bin', 'b.bin', 'c.bin'])
  })

  it('all missing size preserves original relative order (stable sort)', () => {
    const files = [
      { filename: 'a.bin' },
      { filename: 'b.bin' },
    ]
    const result = sortModelFiles(files)
    expect(result.map(f => f.filename)).toEqual(['a.bin', 'b.bin'])
  })

  it('size=0 explicit value equals missing', () => {
    const files = [
      { filename: 'a.bin', size: 0 },
      { filename: 'b.bin', size: 100 },
    ]
    const result = sortModelFiles(files)
    expect(result.map(f => f.filename)).toEqual(['b.bin', 'a.bin'])
  })
})

describe('guessQuant', () => {
  it('recognize Q4_K_M', () => {
    expect(guessQuant('model-Q4_K_M.gguf')).toBe('Q4_K_M')
  })

  it('recognize iq4_xs (case-insensitive)', () => {
    expect(guessQuant('model-iq4_xs.gguf')).toBe('IQ4_XS')
    expect(guessQuant('model-IQ4_XS.gguf')).toBe('IQ4_XS')
  })

  it('recognize BF16', () => {
    expect(guessQuant('model-bf16.gguf')).toBe('BF16')
  })

  it('no quantization recognized returns empty string', () => {
    expect(guessQuant('model.gguf')).toBe('')
    expect(guessQuant('README.md')).toBe('')
  })
})

describe('matchLoadedModelSize', () => {
  // Dock in-memory rows (frame ⑳): the loaded router id maps back to the
  // local scan's human size; no match / unknown size drops the segment ('')
  const models = [
    { name: 'Qwen3-4B-Instruct-Q4_K_M', sizeHuman: '2.3 GB' },
    { name: 'Llama-3.2-3B-Q5_K_M', sizeHuman: '2.1 GB' },
    { name: 'NoSize-Model', sizeHuman: '' },
  ]

  it('exact name match returns its size', () => {
    expect(matchLoadedModelSize('Qwen3-4B-Instruct-Q4_K_M', models)).toBe('2.3 GB')
  })

  it('contained-name match (sanitized router id) returns the size', () => {
    expect(matchLoadedModelSize('Qwen3-4B-Instruct-Q4_K_M.gguf', models)).toBe('2.3 GB')
    expect(matchLoadedModelSize('dir/Llama-3.2-3B-Q5_K_M', models)).toBe('2.1 GB')
  })

  it('no match returns empty string', () => {
    expect(matchLoadedModelSize('Gemma-2-2B', models)).toBe('')
  })

  it('match without a usable size returns empty string', () => {
    expect(matchLoadedModelSize('NoSize-Model', models)).toBe('')
  })

  it('empty id returns empty string', () => {
    expect(matchLoadedModelSize('', models)).toBe('')
  })
})

describe('selectedBytes', () => {
  // Detail-page sticky bar (draft A10/B10 "已选 1 个 · 2.3 GB"): sums the
  // selected files' sizes without inventing weight for unknown/unsized entries
  const files = [
    { filename: 'q4_k_m.gguf', size: 2_300_000_000 },
    { filename: 'q8_0.gguf', size: 4_100_000_000 },
    { filename: 'mmproj.gguf', size: 400_000_000 },
    { filename: 'README.md' },
  ]

  it('sums the selected files sizes', () => {
    expect(selectedBytes(['q4_k_m.gguf'], files)).toBe(2_300_000_000)
    expect(selectedBytes(['q4_k_m.gguf', 'mmproj.gguf'], files)).toBe(2_700_000_000)
  })

  it('unknown filenames and missing sizes contribute 0', () => {
    expect(selectedBytes(['README.md'], files)).toBe(0)
    expect(selectedBytes(['not-in-list.gguf'], files)).toBe(0)
    expect(selectedBytes(['q4_k_m.gguf', 'not-in-list.gguf'], files)).toBe(2_300_000_000)
  })

  it('empty selection returns 0', () => {
    expect(selectedBytes([], files)).toBe(0)
  })
})

describe('isMMProjFile', () => {
  // Mirrors core/preset.go's same-directory auto-detect: lowercase name starts
  // with "mmproj" AND ends with ".gguf"
  it('recognizes mmproj gguf files (case-insensitive)', () => {
    expect(isMMProjFile('mmproj-f16.gguf')).toBe(true)
    expect(isMMProjFile('mmproj.gguf')).toBe(true)
    expect(isMMProjFile('MMPROJ-F16.GGUF')).toBe(true)
    expect(isMMProjFile('MmProj-Vision.Gguf')).toBe(true)
  })

  it('rejects non-mmproj and non-gguf names', () => {
    expect(isMMProjFile('model-q4_k_m.gguf')).toBe(false)
    expect(isMMProjFile('mmproj.txt')).toBe(false)
    expect(isMMProjFile('notmmproj-f16.gguf')).toBe(false)
    expect(isMMProjFile('')).toBe(false)
  })
})

describe('localLibraryStats', () => {
  // My Models summary card (draft B9 "模型 3 个 · 占用 2.9 GB"): scan length
  // plus byte total; missing/negative sizes count as 0
  it('counts models and sums sizes', () => {
    const models = [{ sizeBytes: 2_300_000_000 }, { sizeBytes: 600_000_000 }, { sizeBytes: 500 }]
    expect(localLibraryStats(models)).toEqual({ count: 3, totalBytes: 2_900_000_500 })
  })

  it('missing and negative sizes are treated as 0', () => {
    expect(localLibraryStats([{ sizeBytes: 100 }, {}, { sizeBytes: -5 }])).toEqual({ count: 3, totalBytes: 100 })
  })

  it('empty library returns zeros', () => {
    expect(localLibraryStats([])).toEqual({ count: 0, totalBytes: 0 })
  })
})
