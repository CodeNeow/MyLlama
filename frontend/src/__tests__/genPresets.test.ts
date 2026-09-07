import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import {
  BUILTIN_GEN_PRESETS,
  GEN_PRESETS_CAP,
  GEN_PRESET_DEFAULT_ID,
  clampPresetSamplingField,
  isValidPresetName,
  isValidPresetSampling,
  loadCustomGenPresets,
  loadSelectedGenPresetId,
  mergedGenPresets,
  persistCustomGenPresets,
  persistSelectedGenPresetId,
  presetOverridesSampling,
  presetSamplingFields,
  removeCustomPreset,
  resolveGenPreset,
  withNewCustomPreset,
  type GenPreset,
} from '../lib/genPresets'

const balanced = { temperature: 0.7, topP: 0.9, topK: 40, repeatPenalty: 1.1 }

describe('builtin generation presets', () => {
  it('carries the four builtin samplings plus the default no-override entry', () => {
    const byId = new Map(BUILTIN_GEN_PRESETS.map((p) => [p.id, p]))
    expect(byId.get('default')?.sampling).toBeUndefined()
    expect(byId.get('precise')?.sampling).toEqual({ temperature: 0.3, topP: 0.8, topK: 20, repeatPenalty: 1.1 })
    expect(byId.get('balanced')?.sampling).toEqual({ temperature: 0.7, topP: 0.9, topK: 40, repeatPenalty: 1.1 })
    expect(byId.get('creative')?.sampling).toEqual({ temperature: 1.0, topP: 0.95, topK: 60, repeatPenalty: 1.05 })
    expect(byId.get('random')?.sampling).toEqual({ temperature: 1.2, topP: 1.0, topK: 80, repeatPenalty: 1.0 })
  })

  it('orders the dropdown: default first, then precise/balanced/creative/random', () => {
    expect(BUILTIN_GEN_PRESETS.map((p) => p.id)).toEqual([
      'default',
      'precise',
      'balanced',
      'creative',
      'random',
    ])
  })
})

describe('presetSamplingFields / presetOverridesSampling', () => {
  it('assembles the llama.cpp request field names for non-default presets', () => {
    const fields = presetSamplingFields(BUILTIN_GEN_PRESETS[2])
    expect(fields).toEqual({ temperature: 0.7, top_p: 0.9, top_k: 40, repeat_penalty: 1.1 })
    expect(presetOverridesSampling(BUILTIN_GEN_PRESETS[2])).toBe(true)
  })

  it('returns null and no-override for the default preset', () => {
    const def = BUILTIN_GEN_PRESETS[0]
    expect(presetSamplingFields(def)).toBeNull()
    expect(presetOverridesSampling(def)).toBe(false)
  })

  it('a custom preset without sampling never counts as an override', () => {
    expect(presetOverridesSampling({ id: 'custom-x', name: 'x' })).toBe(false)
  })
})

describe('withNewCustomPreset / removeCustomPreset', () => {
  it('appends a trimmed custom preset with a unique id', () => {
    const r = withNewCustomPreset([], '  我 的 预设  ', balanced)
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.list).toHaveLength(1)
      expect(r.preset.name).toBe('我 的 预设')
      expect(r.preset.sampling).toEqual(balanced)
      expect(r.preset.id).toMatch(/^custom-/)
    }
  })

  it('rejects blank, oversized, and control-character names', () => {
    expect(withNewCustomPreset([], '   ', balanced).ok).toBe(false)
    expect(withNewCustomPreset([], 'x'.repeat(41), balanced).ok).toBe(false)
    expect(withNewCustomPreset([], 'bad\nname', balanced).ok).toBe(false)
    expect(isValidPresetName('ok name_42')).toBe(true)
    expect(isValidPresetName('ok\tname')).toBe(false)
  })

  it('rejects out-of-range sampling values', () => {
    expect(isValidPresetSampling({ ...balanced, temperature: 2.5 })).toBe(false)
    expect(isValidPresetSampling({ ...balanced, topP: 1.5 })).toBe(false)
    expect(isValidPresetSampling({ ...balanced, topK: 40.5 })).toBe(false)
    // The save-as editor's repeat-penalty floor is 0.5 (llama.cpp accepts <1
    // as an anti-repetition boost); below that is invalid.
    expect(isValidPresetSampling({ ...balanced, repeatPenalty: 0.4 })).toBe(false)
    expect(isValidPresetSampling({ ...balanced, repeatPenalty: 0.5 })).toBe(true)
    expect(withNewCustomPreset([], 'bad-sampling', { ...balanced, temperature: -1 }).ok).toBe(false)
    // The defensive out-of-range rejection carries its own reason.
    const r = withNewCustomPreset([], 'bad', { ...balanced, repeatPenalty: 0.1 })
    expect(r).toEqual({ ok: false, reason: 'sampling' })
  })

  it('enforces the 20-custom-preset cap', () => {
    const full: GenPreset[] = Array.from({ length: GEN_PRESETS_CAP }, (_, i) => ({
      id: `custom-${i}`,
      name: `p${i}`,
      sampling: balanced,
    }))
    expect(withNewCustomPreset(full, 'one more', balanced)).toEqual({ ok: false, reason: 'cap' })
    const short = full.slice(0, GEN_PRESETS_CAP - 1)
    const r = withNewCustomPreset(short, 'fits', balanced)
    expect(r.ok).toBe(true)
  })

  it('removeCustomPreset drops only the matching id', () => {
    const list: GenPreset[] = [
      { id: 'custom-a', name: 'a', sampling: balanced },
      { id: 'custom-b', name: 'b', sampling: balanced },
    ]
    const rest = removeCustomPreset(list, 'custom-a')
    expect(rest.map((p) => p.id)).toEqual(['custom-b'])
    expect(removeCustomPreset(list, GEN_PRESET_DEFAULT_ID)).toEqual(list)
  })
})

describe('resolveGenPreset / mergedGenPresets', () => {
  it('falls back to the default preset for unknown ids (deleted custom)', () => {
    const resolved = resolveGenPreset(BUILTIN_GEN_PRESETS, 'custom-gone')
    expect(resolved.id).toBe(GEN_PRESET_DEFAULT_ID)
    expect(resolveGenPreset(BUILTIN_GEN_PRESETS, 'precise').id).toBe('precise')
  })

  it('merges builtins first and customs after', () => {
    const merged = mergedGenPresets([{ id: 'custom-1', name: 'mine', sampling: balanced }])
    expect(merged.map((p) => p.id)).toEqual(['default', 'precise', 'balanced', 'creative', 'random', 'custom-1'])
  })
})

describe('clampPresetSamplingField (save-as editor clamping)', () => {
  it('keeps in-range values and snaps onto the step grid', () => {
    expect(clampPresetSamplingField('temperature', 0.55)).toBe(0.55)
    expect(clampPresetSamplingField('temperature', 0.03)).toBe(0.05)
    expect(clampPresetSamplingField('topP', 0.914)).toBe(0.91)
    expect(clampPresetSamplingField('topK', 40)).toBe(40)
    expect(clampPresetSamplingField('repeatPenalty', 1.234)).toBe(1.23)
  })

  it('clamps into the per-field bounds (repeat_penalty floor 0.5)', () => {
    expect(clampPresetSamplingField('temperature', -1)).toBe(0)
    expect(clampPresetSamplingField('temperature', 3)).toBe(2)
    expect(clampPresetSamplingField('topP', 1.7)).toBe(1)
    expect(clampPresetSamplingField('topK', 500)).toBe(200)
    expect(clampPresetSamplingField('topK', 7.6)).toBe(8)
    expect(clampPresetSamplingField('repeatPenalty', 0.1)).toBe(0.5)
    expect(clampPresetSamplingField('repeatPenalty', 9)).toBe(2)
    expect(clampPresetSamplingField('temperature', NaN)).toBe(0)
  })

  it('a clamped draft always passes validation and saves the edited values', () => {
    // Dialog flow: start from 均衡, edit temperature to 0.55 (clamped input),
    // save-as under a new name — the custom preset carries the edited value.
    const draft = {
      ...balanced,
      temperature: clampPresetSamplingField('temperature', 0.55),
      repeatPenalty: clampPresetSamplingField('repeatPenalty', 0.8),
    }
    expect(isValidPresetSampling(draft)).toBe(true)
    const r = withNewCustomPreset([], 'my 0.55', draft)
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.preset.sampling).toEqual({ temperature: 0.55, topP: 0.9, topK: 40, repeatPenalty: 0.8 })
    }
  })
})

// ─── localStorage round-trips (jsdom) ───────────────────────────────────────

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  localStorage.clear()
})

describe('genPreset persistence', () => {
  it('round-trips the custom list and filters corrupt entries on load', () => {
    persistCustomGenPresets([{ id: 'custom-1', name: 'mine', sampling: balanced }])
    expect(loadCustomGenPresets()).toEqual([{ id: 'custom-1', name: 'mine', sampling: balanced }])

    persistCustomGenPresets([{ id: 'custom-2', name: 'no sampling' }, { id: 'custom-3', name: 'ok', sampling: balanced }])
    expect(loadCustomGenPresets().map((p) => p.id)).toEqual(['custom-3'])
  })

  it('loadCustomGenPresets falls back to empty on corrupt JSON', () => {
    localStorage.setItem('llama-desktop-chat-gen-presets', '{not json')
    expect(loadCustomGenPresets()).toEqual([])
  })

  it('round-trips the selected id and defaults to "default"', () => {
    expect(loadSelectedGenPresetId()).toBe('default')
    persistSelectedGenPresetId('precise')
    expect(loadSelectedGenPresetId()).toBe('precise')
  })
})
