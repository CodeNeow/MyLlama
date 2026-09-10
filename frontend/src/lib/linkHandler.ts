import { Browser } from '@wailsio/runtime'

/**
 * Absolute http(s) URL detection for rendered-markdown links. Only a scheme
 * prefix is checked (case-insensitive, matching browser scheme parsing):
 * protocol-relative //host, javascript:, mailto:, #anchors, relative paths
 * and the empty string all fall through to null.
 */
const ABSOLUTE_HTTP_URL_RE = /^https?:\/\//i

/**
 * Return the href when it is an absolute http(s) URL, null otherwise.
 * Pure decision core of handleLinkClick so the policy is unit-testable
 * without a DOM.
 */
export function externalUrlFor(href: string): string | null {
  return ABSOLUTE_HTTP_URL_RE.test(href) ? href : null
}

/**
 * Shared interception core for every link gesture on containers that render
 * untrusted markdown (model description, chat messages, in-app docs): the
 * WebView must never navigate. Absolute http(s) links open in the system
 * browser via the Wails runtime (Browser.OpenURL), every other href
 * (relative, #anchor, javascript:, mailto:, empty) is silently blocked.
 */
function interceptLinkNavigation(event: MouseEvent): void {
  // Deepest element under the pointer; text nodes are never event targets in
  // the DOM event model, but guard non-element targets defensively.
  const target = event.target instanceof Element ? event.target : null
  const anchor = target?.closest('a')
  if (!anchor) return
  // Always block the WebView navigation, even when the href turns out to be
  // unopenable — a link activation must never replace the app UI.
  event.preventDefault()
  const external = externalUrlFor(anchor.getAttribute('href') ?? '')
  if (external) Browser.OpenURL(external)
}

/**
 * Delegated click handler for the untrusted-markdown containers: bind on the
 * container with @click — every left click that lands inside an anchor is
 * intercepted first (see interceptLinkNavigation for the policy).
 */
export function handleLinkClick(event: MouseEvent): void {
  interceptLinkNavigation(event)
}

/**
 * Delegated middle-button handler: bind on the container with @auxclick.
 * Middle clicks fire auxclick (button === 1), never click, so the click
 * handler alone leaves an auxiliary-navigation surface. Inside the WebView
 * the outcome is engine-defined: relative / #anchor hrefs resolve against
 * the app's internal origin and land on the asset handler's 404 (no script
 * execution surface, but still a navigation leak), and an http(s) href has
 * no meaningful "open in new tab" concept here. Same policy as the left
 * click: http(s) opens in the system browser, everything else is blocked.
 */
export function handleLinkAuxClick(event: MouseEvent): void {
  if (event.button !== 1) return
  interceptLinkNavigation(event)
}

/**
 * Delegated dragstart handler: bind on the container with @dragstart.
 * Anchors are natively draggable, and a Chromium-based WebView navigates to
 * a link dropped back onto the page (the drop default action) — the same
 * navigation surface the click/auxclick handlers close, reachable without
 * any click. Cancelling the drag at its source kills both the in-window
 * drop-navigate and the drag-out gesture (which for http(s) would only
 * duplicate Browser.OpenURL anyway); the links stay clickable.
 */
export function handleLinkDragStart(event: DragEvent): void {
  const target = event.target instanceof Element ? event.target : null
  if (target?.closest('a')) event.preventDefault()
}
