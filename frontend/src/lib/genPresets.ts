/**
 * Chat generation presets (pure helpers + localStorage persistence).
 *
 * A generation preset is a named sampling-parameters quick pick shown next to
 * the chat model capsule. Selecting a non-default preset makes Chat attach
 * temperature / top_p / top_k / repeat_penalty override fields to the
 * /v1/chat/completions body (all four are accepted by llama.cpp's OpenAI
 * endpoint — server-schema.cpp registers field_num("temperature"|"top_p"|
 * "top_k"|"repeat_penalty") and oaicompat_chat_params_parse copies remaining
 * body properties through). The "default" preset means "no override": the
 * request carries no sampling fields and llama-server applies the per-model
 * parameters saved in ModelSettings.
 *
 * Builtin definitions are i18n-free (nameKey); custom presets are user-named
 * and persisted in localStorage with a hard cap.
 */

/** The four sampling override fields of a preset (llama.cpp request shape). */
export interface GenPresetSampling {
  temperature: number
  topP: number
  topK: number
  repeatPenalty: number
}

/** One generation preset. `sampling === undefined` means the default (no override). */
export interface GenPreset {
  id: string
  /** Display name; builtins resolve it through nameKey at the UI layer. */
  name?: string
  /** i18n key of the display name (builtin presets only). */
  nameKey?: string
  builtin?: boolean
  sampling?: GenPresetSampling
}

/** localStorage key for user-saved custom presets. */
const CUSTOM_KEY = 'llama-desktop-chat-gen-presets'
/** localStorage key for the selected preset id. */
const SELECTED_KEY = 'llama-desktop-chat-gen-preset'

/** The id of the "no override" preset (llama-server saved params apply). */
export const GEN_PRESET_DEFAULT_ID = 'default'

/** Hard cap on user-saved custom presets (oldest kept, newest beyond cap rejected). */
export const GEN_PRESETS_CAP = 20

/**
 * Builtin presets (borrowed from the unsloth generation-preset vocabulary):
 * precise / balanced / creative / random, plus the default no-override entry.
 * Order defines the dropdown order.
 */
export const BUILTIN_GEN_PRESETS: GenPreset[] = [
  { id: 'default', nameKey: 'chat.presetDefault', builtin: true },
  { id: 'precise', nameKey: 'chat.presetPrecise', builtin: true, sampling: { temperature: 0.3, topP: 0.8, topK: 20, repeatPenalty: 1.1 } },
  { id: 'balanced', nameKey: 'chat.presetBalanced', builtin: true, sampling: { temperature: 0.7, topP: 0.9, topK: 40, repeatPenalty: 1.1 } },
  { id: 'creative', nameKey: 'chat.presetCreative', builtin: true, sampling: { temperature: 1.0, topP: 0.95, topK: 60, repeatPenalty: 1.05 } },
  { id: 'random', nameKey: 'chat.presetRandom', builtin: true, sampling: { temperature: 1.2, topP: 1.0, topK: 80, repeatPenalty: 1.0 } },
]

/** Sampling ranges accepted for save-as (mirrors the params editor bounds; the
 * repeat-penalty floor is 0.5 — llama.cpp accepts <1 as an anti-repetition
 * boost, 1.0 disables the penalty). */
export function isValidPresetSampling(s: GenPresetSampling | undefined): boolean {
  if (!s) return false
  return (
    Number.isFinite(s.temperature) && s.temperature >= 0 && s.temperature <= 2 &&
    Number.isFinite(s.topP) && s.topP >= 0 && s.topP <= 1 &&
    Number.isFinite(s.topK) && Number.isInteger(s.topK) && s.topK >= 0 &&
    Number.isFinite(s.repeatPenalty) && s.repeatPenalty >= 0.5 && s.repeatPenalty <= 2
  )
}

/** Per-field input bounds + steps of the save-as editor (dialog renders these). */
export const GEN_SAMPLING_BOUNDS: Record<keyof GenPresetSampling, { min: number; max: number; step: number }> = {
  temperature: { min: 0, max: 2, step: 0.05 },
  topP: { min: 0, max: 1, step: 0.01 },
  topK: { min: 0, max: 200, step: 1 },
  repeatPenalty: { min: 0.5, max: 2, step: 0.01 },
}

/**
 * clampPresetSamplingField snaps one user-typed sampling value onto its step
 * grid and clamps it into the field's bounds (the save-as editor clamps on
 * change, so persisted values always pass isValidPresetSampling). top_k snaps
 * to an integer; float fields keep the step's decimal count to kill binary
 * dust. Non-finite input snaps to the lower bound. Pure.
 */
export function clampPresetSamplingField<K extends keyof GenPresetSampling>(field: K, value: number): number {
  const b = GEN_SAMPLING_BOUNDS[field]
  if (!Number.isFinite(value)) return b.min
  const snapped = Math.round((value - b.min) / b.step) * b.step + b.min
  const stepStr = String(b.step)
  const dot = stepStr.indexOf('.')
  const decimals = dot < 0 ? 0 : stepStr.length - dot - 1
  const fixed = Number(snapped.toFixed(decimals + 1))
  return Math.min(Math.max(fixed, b.min), b.max)
}

/**
 * isValidPresetName validates a user-entered preset name: trimmed non-empty,
 * at most 40 characters, no control characters or line breaks (localStorage
 * payload hygiene; the name renders verbatim in the dropdown).
 */
export function isValidPresetName(name: string): boolean {
  const trimmed = name.trim()
  if (!trimmed || trimmed.length > 40) return false
  // eslint-disable-next-line no-control-regex
  return !/[\u0000-\u001f\u007f]/.test(trimmed)
}

/** The full merged option list: builtins first, then the user's customs in save order. */
export function mergedGenPresets(customs: GenPreset[]): GenPreset[] {
  return [...BUILTIN_GEN_PRESETS, ...(customs ?? [])]
}

/** Result of a save-as attempt (pure: the caller owns persistence). */
export type AddPresetResult =
  | { ok: true; list: GenPreset[]; preset: GenPreset }
  | { ok: false; reason: 'name' | 'cap' | 'sampling' }

/**
 * withNewCustomPreset appends a custom preset carrying the given sampling
 * values: rejects blank/oversized names ('name'), out-of-range sampling
 * ('sampling' — the dialog clamps on change, so this is a defensive branch)
 * and the 20-entry cap ('cap'). The new preset becomes appended last; the
 * caller persists the returned list and selects `preset.id`.
 */
export function withNewCustomPreset(
  existing: GenPreset[],
  name: string,
  sampling: GenPresetSampling
): AddPresetResult {
  if (!isValidPresetName(name)) return { ok: false, reason: 'name' }
  if (!isValidPresetSampling(sampling)) return { ok: false, reason: 'sampling' }
  if ((existing?.length ?? 0) >= GEN_PRESETS_CAP) return { ok: false, reason: 'cap' }
  const preset: GenPreset = {
    id: `custom-${Date.now()}-${Math.floor(Math.random() * 1e6)}`,
    name: name.trim(),
    sampling: { ...sampling },
  }
  return { ok: true, list: [...(existing ?? []), preset], preset }
}

/** removeCustomPreset drops one custom preset by id (builtins are unremovable). Pure. */
export function removeCustomPreset(existing: GenPreset[], id: string): GenPreset[] {
  return (existing ?? []).filter((p) => p.id !== id)
}

/**
 * resolveGenPreset looks a selected id up in the merged list, falling back to
 * the default preset when the id is unknown (e.g. the custom preset was
 * deleted on another machine state). Pure.
 */
export function resolveGenPreset(list: GenPreset[], id: string): GenPreset {
  const found = (list ?? []).find((p) => p.id === id)
  if (found) return found
  return BUILTIN_GEN_PRESETS[0]
}

/**
 * The request shape of a preset's sampling fields, named exactly as llama.cpp's
 * OpenAI endpoint reads them (server-schema.cpp): temperature / top_p / top_k /
 * repeat_penalty. Returns null for the default preset (no override fields).
 */
export function presetSamplingFields(preset: GenPreset): Record<string, number> | null {
  if (!preset.sampling) return null
  const s = preset.sampling
  return { temperature: s.temperature, top_p: s.topP, top_k: s.topK, repeat_penalty: s.repeatPenalty }
}

/**
 * Whether the selected preset attaches sampling override fields to the chat
 * request: every preset except `default` does.
 */
export function presetOverridesSampling(preset: GenPreset): boolean {
  return preset.id !== GEN_PRESET_DEFAULT_ID && preset.sampling !== undefined
}

// ─── localStorage (side-effectful, isolated below) ─────────────────────────

/** Read the saved custom presets; corrupt payloads fall back to an empty list. */
export function loadCustomGenPresets(): GenPreset[] {
  try {
    const raw = localStorage.getItem(CUSTOM_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(
      (p): p is GenPreset =>
        p && typeof p.id === 'string' && typeof p.name === 'string' && isValidPresetSampling(p.sampling)
    )
  } catch {
    return []
  }
}

/** Persist the custom preset list; write failures are swallowed (chat keeps working). */
export function persistCustomGenPresets(list: GenPreset[]): void {
  try {
    localStorage.setItem(CUSTOM_KEY, JSON.stringify(list ?? []))
  } catch {
    // Storage full / unavailable: keep the in-memory list authoritative.
  }
}

/** Read the selected preset id; missing values fall back to the default preset. */
export function loadSelectedGenPresetId(): string {
  try {
    return localStorage.getItem(SELECTED_KEY) || GEN_PRESET_DEFAULT_ID
  } catch {
    return GEN_PRESET_DEFAULT_ID
  }
}

/** Persist the selected preset id; write failures are swallowed. */
export function persistSelectedGenPresetId(id: string): void {
  try {
    localStorage.setItem(SELECTED_KEY, id)
  } catch {
    // Silent fallback
  }
}
