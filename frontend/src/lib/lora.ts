/**
 * LoRA adapter management pure helpers (ModelSettings LoRA tab).
 *
 * The adapter list shown in the UI is the merge of two sources: the scanned
 * .gguf files in the LoRA directory (LoraInfo, disk truth) and the persisted
 * per-model references (LoraRef, config truth). These helpers keep the merge,
 * the scale clamping and the enabled-count aggregation in one testable place;
 * the component only wires them to state.
 */

import type { LoraInfo, LoraRef } from '../wails'

/** One UI row: a scanned adapter overlaid with its persisted mount state. */
export interface LoraRow {
  name: string
  sizeHuman: string
  alpha: number
  hasAlpha: boolean
  arch: string
  /** false when the GGUF header does not identify the file as a LoRA adapter */
  valid: boolean
  /** false when a persisted ref points at a file no longer in the LoRA directory */
  onDisk: boolean
  scale: number
  enabled: boolean
}

/** Upper bound of the upstream --lora-scaled weight the UI accepts. */
export const LORA_SCALE_MAX = 4

/** Default weight llama-server applies to --lora (arg.cpp pushes 1.0). */
export const LORA_SCALE_DEFAULT = 1

/**
 * clampLoraScale normalizes a user-typed scale into the supported [0, 4]
 * range: non-finite input falls back to the default, values outside the range
 * are clamped to the nearest bound, and the result is rounded to 2 decimals
 * so a 0.05-step input never carries float noise (0.30000000000000004).
 */
export function clampLoraScale(value: number): number {
  if (!Number.isFinite(value)) return LORA_SCALE_DEFAULT
  const clamped = Math.min(LORA_SCALE_MAX, Math.max(0, value))
  return Math.round(clamped * 100) / 100
}

/**
 * mergeLoraRows builds the UI rows: every scanned file is a row (sorted as the
 * backend delivered it — by name), overlaid with its persisted ref state when
 * one exists; persisted refs whose file disappeared from the directory are
 * kept as onDisk=false rows so the user can see and drop them.
 * Matching is by exact file name (names are validated plain file names on the
 * backend).
 */
export function mergeLoraRows(scan: LoraInfo[], refs: LoraRef[]): LoraRow[] {
  const byName = new Map<string, LoraRef>()
  for (const ref of refs ?? []) {
    byName.set(ref.name, ref)
  }
  const rows: LoraRow[] = (scan ?? []).map((info) => {
    const ref = byName.get(info.name)
    return {
      name: info.name,
      sizeHuman: info.sizeHuman || '—',
      alpha: info.alpha,
      hasAlpha: info.hasAlpha,
      arch: info.arch,
      valid: info.valid,
      onDisk: true,
      scale: clampLoraScale(ref ? ref.scale : LORA_SCALE_DEFAULT),
      enabled: ref ? ref.enabled : false,
    }
  })
  const scanned = new Set((scan ?? []).map((i) => i.name))
  for (const ref of refs ?? []) {
    if (scanned.has(ref.name)) continue
    rows.push({
      name: ref.name,
      sizeHuman: '—',
      alpha: 0,
      hasAlpha: false,
      arch: '',
      valid: false,
      onDisk: false,
      scale: clampLoraScale(ref.scale),
      enabled: ref.enabled,
    })
  }
  return rows
}

/**
 * rowsToRefs converts the full UI row list back into the persisted refs shape:
 * every row is kept (the enabled switch must survive reloads), rows whose file
 * vanished or whose header does not identify an adapter stay listed but are
 * never mounted by the backend (enabled=true would fail validation upstream).
 */
export function rowsToRefs(rows: LoraRow[]): LoraRef[] {
  return rows.map((row) => ({ name: row.name, scale: row.scale, enabled: row.enabled }))
}

/**
 * enabledLoraRefs picks the refs the backend actually mounts: enabled AND
 * present on disk AND identified as adapters. Used for the summary line and
 * the runtime hot-apply count.
 */
export function enabledLoraRefs(rows: LoraRow[]): LoraRef[] {
  return rows
    .filter((row) => row.enabled && row.onDisk && row.valid)
    .map((row) => ({ name: row.name, scale: row.scale, enabled: true }))
}

/** countEnabled is the summary-line count of mountable enabled rows. */
export function countEnabled(rows: LoraRow[]): number {
  return rows.reduce((n, row) => (row.enabled && row.onDisk && row.valid ? n + 1 : n), 0)
}
