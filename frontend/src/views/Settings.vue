<template>
  <div class="page">
    <div class="sticky-top">
      <div class="page-header">
        <h1 class="page-title">{{ t('settings.title') }}</h1>
        <p class="page-subtitle">{{ t('settings.subtitle') }}</p>
      </div>
    </div>

    <!-- Help & tutorial entry: the former sixth navigation destination moved
         here from the nav bars (mobile redesign IA, v1 mobile design draft
         frame ⑤). The /docs route itself is unchanged; the card sits above the
         setting groups so it is reachable from every scroll position. On the
         tablet tracks (draft frames A16/B16) CSS moves this same node to the
         END of the page and restyles it into the draft's surface row card —
         phone and desktop keep the gradient hero at the top. -->
    <router-link to="/docs" class="docs-entry">
      <span class="docs-entry-icon" v-html="DOCS_ICON"></span>
      <span class="docs-entry-text">
        <span class="docs-entry-title">{{ t('settings.docsEntry') }}</span>
        <span class="docs-entry-sub">{{ docsEntrySub }}</span>
      </span>
      <svg class="docs-entry-arrow" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>
      <!-- Phone tail (frame ⑯): the chevron SVG becomes a plain → glyph -->
      <span v-if="isPhone" class="docs-entry-arrow-glyph" aria-hidden="true">→</span>
    </router-link>

    <!-- Device island (frame ⑤): OS · arch · llama-server acceleration build ·
           app version, all read from existing bindings. Shown on desktop and
           mobile alike — it is a capability summary, not a phone-only widget. -->
    <section class="device-card" :aria-label="t('settings.device')">
      <div class="device-label">{{ t('settings.device') }}</div>
      <div class="device-chips">
        <span class="device-chip">{{ osName }}</span>
        <span v-if="archLabel" class="device-chip">{{ archLabel }}</span>
        <span class="device-chip">{{ accelLabel }}</span>
        <span class="device-chip">{{ versionChip }}</span>
      </div>
    </section>

    <!-- ─── Group: appearance & preferences (frame ⑤ group 1) ───
         Theme toggle + language + download source. Same handlers and guard
         semantics as before; only the containers changed (rows in a rounded
         group instead of tabbed sections). -->
    <section class="settings-group group-appearance" :aria-label="t('settings.groupAppearance')">
      <!-- Theme mode -->
      <div class="group-item">
        <div class="group-row">
          <span class="row-ic ic-indigo" v-html="ICON_MOON"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.themeMode') }}</span>
            <span class="row-sub">{{ t('settings.themeDesc') }}</span>
          </div>
          <div class="row-tail row-tail-switch">
            <div
              class="switch"
              :class="{ on: currentTheme === 'dark' }"
              role="switch"
              :aria-checked="currentTheme === 'dark'"
              :aria-label="t('settings.themeMode')"
              tabindex="0"
              @click="toggleTheme"
              @keydown.enter="toggleTheme"
            >
            </div>
          </div>
        </div>
      </div>

      <!-- Interface language -->
      <div class="group-item">
        <div class="group-row">
          <span class="row-ic ic-emerald" v-html="ICON_GLOBE"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.language') }}</span>
            <span class="row-sub">{{ languageCurrentLabel }}</span>
          </div>
          <!-- Desktop / tablet: compact ThemedSelect in the row tail (design draft F5) -->
          <div v-if="!isPhone" class="row-tail row-tail-select">
            <ThemedSelect
              :model-value="appConfig.language"
              :options="languageOptions"
              :placeholder="t('settings.language')"
              variant="toolbar"
              :label="t('settings.language')"
              @update:model-value="setLanguagePref"
            />
          </div>
          <!-- Phone tail (frame ⑯): compact select showing current language label -->
          <div v-else class="row-tail row-tail-select">
            <ThemedSelect
              :model-value="appConfig.language"
              :options="languageOptions"
              :placeholder="t('settings.language')"
              variant="field"
              menu-class="settings-sheet-menu"
              :label="t('settings.language')"
              @update:model-value="setLanguagePref"
            />
          </div>
        </div>
        <p v-if="languageError" class="row-error">{{ languageError }}</p>
      </div>

      <!-- Model download source -->
      <div class="group-item">
        <div class="group-row">
          <span class="row-ic ic-amber" v-html="ICON_DOWNLOAD"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.downloadSource') }}</span>
            <span class="row-sub">{{ sourceCurrentLabel }}</span>
          </div>
          <!-- Desktop / tablet: compact ThemedSelect in the row tail (design draft F5) -->
          <div v-if="!isPhone" class="row-tail row-tail-select">
            <ThemedSelect
              :model-value="downloadSource"
              :options="sourceOptions"
              :placeholder="t('settings.downloadSource')"
              variant="toolbar"
              :label="t('settings.downloadSource')"
              @update:model-value="setSource"
            />
          </div>
          <!-- Phone tail (frame ⑯): compact select showing current source label -->
          <div v-else class="row-tail row-tail-select">
            <ThemedSelect
              :model-value="downloadSource"
              :options="sourceOptions"
              :placeholder="t('settings.downloadSource')"
              variant="field"
              menu-class="settings-sheet-menu"
              :label="t('settings.downloadSource')"
              @update:model-value="setSource"
            />
          </div>
        </div>
        <p class="row-foot">{{ t('settings.sourceHint') }}</p>
        <p v-if="sourceError" class="row-error">{{ sourceError }}</p>
      </div>
    </section>

    <!-- ─── Group: directories & services (frame ⑤ group 2) ───
         Download paths + service access scope + API key + serving GPU +
         Windows-only tray / API-route toggles. Platform gates are unchanged:
         showTray / showApiRoute / showGpu stay OS-scoped helpers. -->
    <section class="settings-group group-service" :aria-label="t('settings.groupService')">
      <!-- llama.cpp download path -->
      <div class="group-item" :aria-label="t('settings.directories')">
        <div class="group-row">
          <span class="row-ic ic-violet" v-html="ICON_FOLDER"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.llamaCppDownloadDir') }}</span>
            <span class="row-sub">{{ t('settings.llamaCppDownloadDirDesc') }}</span>
          </div>
        </div>
        <div class="dir-path-row">
          <div class="dir-path">{{ appConfig.llamaCppDownloadDir }}</div>
          <!-- Android has no native directory picker (the Browse* bindings error
               there): the browse buttons are hidden, paths stay read-only -->
          <button v-if="!isAndroid" class="dir-btn" type="button" @click="chooseLlamaCppDownloadDir">{{ t('settings.choose') }}</button>
        </div>
      </div>

      <!-- Model download path -->
      <div class="group-item">
        <div class="group-row">
          <span class="row-ic ic-violet" v-html="ICON_FOLDER"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.modelDownloadDir') }}</span>
            <!-- Android phone (frame ⑯): sub carries the scanned model count
                 and total size; other tiers keep the plain description -->
            <span class="row-sub">{{ modelDirSub }}</span>
          </div>
        </div>
        <div class="dir-path-row">
          <div class="dir-path">{{ appConfig.modelDownloadDir }}</div>
          <button v-if="!isAndroid" class="dir-btn" type="button" @click="chooseModelDownloadDir">{{ t('settings.choose') }}</button>
        </div>
        <!-- Android: no system folder picker; both paths are app-managed and
             read-only, so the rows stay informational with an explicit hint -->
        <p v-if="isAndroid" class="row-foot">{{ t('settings.dirsAndroidHint') }}</p>
        <p class="row-foot">{{ t('settings.directoriesHint') }}</p>
      </div>

      <!-- Server access scope -->
      <div class="group-item">
        <div class="group-row">
          <span class="row-ic ic-sky" v-html="ICON_ACCESS"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.accessScope') }}</span>
            <span class="row-sub">{{ t('settings.accessDesc') }}</span>
          </div>
          <!-- Desktop / tablet: compact ThemedSelect in the row tail (design draft F5) -->
          <div v-if="!isPhone" class="row-tail row-tail-select">
            <ThemedSelect
              :model-value="appConfig.serverAccessMode"
              :options="accessOptions"
              :placeholder="t('settings.accessScope')"
              variant="toolbar"
              :label="t('settings.accessScope')"
              @update:model-value="setAccessScope"
            />
          </div>
          <!-- Phone tail (frame ⑯): compact select showing current access scope -->
          <div v-else class="row-tail row-tail-select">
            <ThemedSelect
              :model-value="appConfig.serverAccessMode"
              :options="accessOptions"
              :placeholder="t('settings.accessScope')"
              variant="field"
              menu-class="settings-sheet-menu"
              :label="t('settings.accessScope')"
              @update:model-value="setAccessScope"
            />
          </div>
        </div>
        <p v-if="accessError" class="row-error">{{ accessError }}</p>
      </div>

      <!-- API key: always visible — it also protects the inference API in
           local mode, not only when the service is exposed to the LAN -->
      <div class="group-item">
        <div class="group-row">
          <span class="row-ic ic-rose" v-html="ICON_KEY"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.apiKey') }}</span>
            <span class="row-sub">{{ t('settings.apiKeyDesc') }}</span>
          </div>
          <!-- Desktop / tablet: "设置 ›" button opening a centered dialog (design draft F5) -->
          <button v-if="!isPhone" type="button" class="row-tail-api-key" @click="showApiKeyDialog = true">
            <span>{{ t('settings.apiKeySet') }}</span>
            <span aria-hidden="true">›</span>
          </button>
          <!-- Phone tail (frame ⑯): "未设置（无鉴权）›" / "已设置 ›" -->
          <button v-else type="button" class="row-tail-api-key" @click="showApiKeySheet = true">
            <span>{{ apiKeyInput ? t('settings.apiKeySet') : t('settings.apiKeyNotSet') }}</span>
            <span aria-hidden="true">›</span>
          </button>
        </div>
        <p v-if="apiKeyError" class="row-error">{{ apiKeyError }}</p>

        <!-- Phone API key bottom sheet (frame ⑯) -->
        <div v-if="isPhone && showApiKeySheet" class="api-key-dim" @click.self="showApiKeySheet = false"></div>
        <div v-if="isPhone && showApiKeySheet" class="api-key-sheet">
          <div class="api-key-grab"></div>
          <div class="api-key-sheet-title">{{ t('settings.apiKey') }}</div>
          <input
            v-model="apiKeyInput"
            type="password"
            class="api-key-sheet-input"
            autocomplete="off"
            spellcheck="false"
            :disabled="apiKeySwitching"
            :placeholder="t('settings.apiKeyPlaceholder')"
            @change="saveApiKey"
          />
          <button type="button" class="api-key-done" @click="showApiKeySheet = false">{{ t('api.done') }}</button>
        </div>

        <!-- Desktop API key centered dialog (design draft F5) -->
        <div v-if="!isPhone && showApiKeyDialog" class="api-key-dialog-root" @click.self="showApiKeyDialog = false">
          <div class="api-key-dialog">
            <div class="api-key-dialog-title">{{ t('settings.apiKey') }}</div>
            <input
              v-model="apiKeyInput"
              type="password"
              class="api-key-dialog-input"
              autocomplete="off"
              spellcheck="false"
              :disabled="apiKeySwitching"
              :placeholder="t('settings.apiKeyPlaceholder')"
              @change="saveApiKey"
            />
            <div class="api-key-dialog-actions">
              <button type="button" class="api-key-dialog-cancel" @click="showApiKeyDialog = false">{{ t('api.done') }}</button>
            </div>
          </div>
        </div>
      </div>

      <!-- Inference GPU selection: pins the llama-server child to the chosen
           CUDA device via CUDA_VISIBLE_DEVICES (empty = auto, default device).
           Persisted through the same whole-serverConfig round-trip as the key.
           Windows-only: the backend device pinning no-ops on other platforms. -->
      <template v-if="showGpu">
        <div class="group-item">
          <div class="group-row">
            <span class="row-ic ic-teal" v-html="ICON_GPU"></span>
            <div class="row-text">
              <span class="row-title">{{ t('settings.gpu.label') }}</span>
              <span class="row-sub">{{ t('settings.gpu.desc') }}</span>
            </div>
            <ThemedSelect
              class="gpu-select"
              :model-value="gpuValue"
              :options="gpuOptions"
              :placeholder="t('settings.gpu.auto')"
              :disabled="!gpuDetected || gpuSwitching"
              variant="field"
              :label="t('settings.gpu.label')"
              @update:model-value="onGpuSelected"
            />
          </div>
          <p v-if="!gpuDetected" class="row-foot">{{ t('settings.gpu.none') }}</p>
          <p v-else class="row-foot">{{ t('settings.gpu.hint') }}</p>
          <p v-if="gpuError" class="row-error">{{ gpuError }}</p>
        </div>
      </template>

      <!-- System tray (Windows / macOS only; other platforms exit directly on close) -->
      <template v-if="showTray">
        <div class="group-item">
          <div class="group-row">
            <span class="row-ic ic-slate" v-html="ICON_TRAY"></span>
            <div class="row-text">
              <span class="row-title">{{ t('settings.tray') }}</span>
              <span class="row-sub">{{ t('settings.trayDesc') }}</span>
            </div>
            <div class="row-tail row-tail-switch">
              <div
                class="switch"
                :class="{ on: appConfig.trayEnabled }"
                role="switch"
                :aria-checked="appConfig.trayEnabled"
                :aria-label="t('settings.tray')"
                tabindex="0"
                @click="toggleTray"
                @keydown.enter="toggleTray"
              >
              </div>
            </div>
          </div>
          <!-- systray cannot restart in same process: after disabling, re-enable requires app restart -->
          <p class="row-foot">{{ t('settings.trayHint') }}</p>
          <p v-if="trayError" class="row-error">{{ trayError }}</p>
        </div>
      </template>

      <!-- API route mode (Windows only): restart into headless tray+server mode -->
      <template v-if="showApiRoute">
        <div class="group-item">
          <div class="group-row">
            <span class="row-ic ic-fuchsia" v-html="ICON_SHARE"></span>
            <div class="row-text">
              <span class="row-title">{{ t('settings.apiRouteMode') }}</span>
              <span class="row-sub">{{ t('settings.apiRouteModeDesc') }}</span>
            </div>
            <div class="row-tail row-tail-switch">
              <div
                class="switch"
                :class="{ on: appConfig.apiRouteMode, disabled: !appConfig.trayEnabled || apiRouteDevBlocked }"
                role="switch"
                :aria-checked="appConfig.apiRouteMode"
                :aria-disabled="!appConfig.trayEnabled || apiRouteDevBlocked"
                :aria-label="t('settings.apiRouteMode')"
                tabindex="0"
                @click="toggleApiRouteMode"
                @keydown.enter="toggleApiRouteMode"
              >
              </div>
            </div>
          </div>
          <!-- dev builds refuse the relaunch-based switch entirely: it escapes the wails dev supervisor -->
          <p v-if="apiRouteDevBlocked" class="row-foot">{{ t('settings.apiRouteModeDevBlocked') }}</p>
          <!-- headless mode returns to the GUI via the tray menu only: gate the toggle on the tray setting -->
          <p v-else-if="!appConfig.trayEnabled" class="row-foot">{{ t('settings.apiRouteModeRequiresTray') }}</p>
          <p v-if="apiRouteError" class="row-error">{{ apiRouteError }}</p>
        </div>
      </template>
    </section>

    <!-- ─── Group: about (frame ⑤ group 3) ───
         Updates: visible on every platform. Windows and Android both render
         the in-app check-for-updates action cluster (Windows self-updates via
         the downloaded NSIS installer; Android downloads an APK and the system
         PackageInstaller confirms it); linux/darwin/other fall back to the
         link mode (hint + GitHub Releases link) because the backend
         CheckForUpdateAt gate short-circuits to "no update" on those
         platforms. -->
    <section class="settings-group group-about" :aria-label="t('settings.about')">
      <div class="group-item">
        <div class="group-row">
          <span class="row-ic ic-blue" v-html="ICON_REFRESH"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.update') }}</span>
            <span class="row-sub" :class="{ 'row-sub-ok': updatesLink && isPhone }">{{ updateSub }}</span>
          </div>
          <!-- Windows / Android: in-app check-for-updates action (Windows self-updates
               via the downloaded NSIS installer; Android downloads an APK that the
               system PackageInstaller confirms). Other platforms have no native
               install path, so the action is hidden there. -->
          <div v-if="showCheckActions" class="row-tail update-actions">
            <span v-if="checkError" class="row-error">{{ checkError }}</span>
            <span v-else-if="checkResult && !checkResult.hasUpdate" class="update-latest">{{ t('settings.latest') }}</span>
            <button class="btn-check" :disabled="checking" @click="manualCheck">
              {{ checking ? t('settings.checking') : t('settings.checkUpdate') }}
            </button>
          </div>
          <!-- Phone link mode (frame ⑯): the Releases link IS the row tail;
               the shared external-link handler opens the system browser.
               Only platforms without a check action reach this branch. -->
          <a
            v-else-if="isPhone"
            class="row-tail updates-link"
            href="https://github.com/CodeNeow/llama-cpp-desktop/releases"
            @click="handleLinkClick"
          >{{ t('settings.updateReleasesLink') }} <span aria-hidden="true">↗</span></a>
        </div>
        <!-- Android (all form factors): Releases link as a footnote row — the
             in-app modal handles the actual install path, so the link is
             supplementary, not the primary discovery mechanism. -->
        <p v-if="isAndroid" class="row-foot update-hint">
          <a
            class="hint-link"
            href="https://github.com/CodeNeow/llama-cpp-desktop/releases"
            @click="handleLinkClick"
          >{{ t('settings.updateReleasesLink') }}</a>
        </p>
        <!-- Desktop link mode (no native install path): hint instead of the
             action; the Releases link opens through the shared external-link
             handler (system browser, never in-WebView navigation) -->
        <p v-else-if="!isPhone" class="row-foot update-hint">
          {{ t('settings.updateNotSupported') }}
          <a
            class="hint-link"
            href="https://github.com/CodeNeow/llama-cpp-desktop/releases"
            @click="handleLinkClick"
          >{{ t('settings.updateReleasesLink') }}</a>
        </p>
      </div>

      <!-- About: version / license / repository. Desktop keeps the three
           label/value rows — the repo URL is plain selectable text, NOT an
           <a> link: clicking a link would navigate the WebView away from the
           app. -->
      <div v-if="!isPhone" class="group-item">
        <div class="about-row">
          <span class="about-label">{{ t('settings.version') }}</span>
          <span class="about-value">{{ appVersion || '—' }}</span>
        </div>
        <div class="about-row">
          <span class="about-label">{{ t('settings.license') }}</span>
          <span class="about-value">GPL-3.0</span>
        </div>
        <div class="about-row">
          <span class="about-label">{{ t('settings.repo') }}</span>
          <span class="about-value about-mono">https://github.com/CodeNeow/llama-cpp-desktop</span>
        </div>
      </div>
      <!-- Phone (frame ⑯): the three rows consolidate into ONE row — label,
           version · license · repo sub line, and a tail that opens the repo
           externally through the shared link handler -->
      <div v-else class="group-item">
        <div class="group-row">
          <span class="row-ic ic-about" v-html="ICON_INFO"></span>
          <div class="row-text">
            <span class="row-title">{{ t('settings.about') }}</span>
            <span class="row-sub">v{{ appVersion || '—' }} · GPL-3.0 · CodeNeow/llama-cpp-desktop</span>
          </div>
          <a
            class="row-tail about-link"
            href="https://github.com/CodeNeow/llama-cpp-desktop"
            :aria-label="t('settings.repo')"
            @click="handleLinkClick"
          >›</a>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { appConfig, setTheme, loadConfig, setDownloadSource as applyDownloadSource, setLanguage as applyLanguage, setServerAccessMode as applyServerAccessMode, setApiKey as applyApiKey, setTrayEnabled as applyTrayEnabled } from '../store'
import { updateState, checkForUpdate } from '../lib/update'
import { getAppVersion, getLlamaCpp, getSystemInfo, getServerConfig, saveServerConfig, browseLlamaCppDownloadDir, browseModelDownloadDir, setApiRouteMode, getModels } from '../wails'
import { accelBuildKey, showTraySetting, showApiRouteSetting, showServingGpuSetting, updateSectionMode, showUpdateCheckActions, usePlatform } from '../lib/platform'
import { handleLinkClick } from '../lib/linkHandler'
import { DOCS_ICON } from '../lib/navigation'
import { docSections } from '../docs/manifest'
import ThemedSelect, { type SelectOption } from '../components/ThemedSelect.vue'
import { formatMB, formatBytes } from '../lib/format'
import { t } from '../lib/i18n'

// ─── Row icons (inline stroke SVGs, mirroring the former section titles) ─────
const ICON_MOON = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>`
const ICON_GLOBE = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>`
const ICON_DOWNLOAD = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>`
const ICON_FOLDER = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>`
const ICON_ACCESS = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/></svg>`
const ICON_KEY = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/></svg>`
const ICON_GPU = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2" ry="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>`
const ICON_TRAY = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="7" width="20" height="14" rx="2" ry="2"/><path d="M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16"/></svg>`
const ICON_SHARE = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/></svg>`
const ICON_REFRESH = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>`
const ICON_INFO = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>`

// ─── Device island (frame ⑤) ─────────────────────────────────────────────────
// OS + arch come from the shared platform state (App.vue wires it from the
// backend getOS() binding); the acceleration label mirrors RuntimeSection:
// prefer the actually detected llama.cpp build (getLlamaCpp().accel), fall
// back to the platform capability guess (accelBuildKey), and keep the
// android arm64 qualifier on the CPU-only label. Version via getAppVersion().
const platform = usePlatform()

// Phone tier (frame ⑯): gates the structural phone variants — the counted
// docs-entry sub, the → entry tail, the consolidated About row and the
// update release-link row. Styling-only phone changes stay in media queries.
const isPhone = computed(() => platform.value.isMobile)

// Docs-entry sub (frame ⑯): the phone tier carries the REAL section count
// from the docs manifest; desktop keeps the plain copy.
const docsEntrySub = computed(() =>
  isPhone.value
    ? t('settings.docsEntrySubCounted', { n: docSections.length })
    : t('settings.docsEntrySub'),
)

const osName = computed(() => {
  switch (platform.value.os) {
    case 'windows':
      return t('settings.os.windows')
    case 'linux':
      return t('settings.os.linux')
    case 'darwin':
      return t('settings.os.darwin')
    case 'android':
      return t('settings.os.android')
    case 'ios':
      return t('settings.os.ios')
    default:
      return t('settings.os.other')
  }
})

// Raw GOARCH string (arm64 / amd64 / ...); hidden entirely while unknown.
const archLabel = computed(() => platform.value.arch)

const llamacppAccel = ref('')

const accelLabel = computed(() => {
  const detected = llamacppAccel.value
  if (detected === 'cuda') return t('runtime.accel.cuda')
  if (detected === 'vulkan') return t('runtime.accel.vulkan')
  if (detected === 'metal') return t('runtime.accel.metal')
  if (detected === 'cpu') return platform.value.isAndroid ? t('runtime.accel.cpuArm64') : t('runtime.accel.cpu')
  const fallback = accelBuildKey(platform.value)
  if (fallback === 'cpu' && platform.value.isAndroid) return t('runtime.accel.cpuArm64')
  return t(`runtime.accel.${fallback}`)
})

const versionChip = computed(() => appVersion.value || '—')

// ─── Theme ───────────────────────────────────────────────────────────────────
const currentTheme = computed({
  get: () => appConfig.theme,
  set: (v) => setTheme(v)
})

function toggleTheme() {
  currentTheme.value = currentTheme.value === 'dark' ? 'light' : 'dark'
}

// ─── Interface language ──────────────────────────────────────────────────────
// Compact segment labels for the row tail; the row sub-line carries the full
// current value (languageAuto / languageZh / languageEn).
const languageOptions = computed(() => [
  { value: 'auto', label: t('settings.languageAutoShort') },
  { value: 'zh', label: t('settings.languageZh') },
  { value: 'en', label: t('settings.languageEn') },
])

const languageCurrentLabel = computed(() => {
  if (appConfig.language === 'zh') return t('settings.languageZh')
  if (appConfig.language === 'en') return t('settings.languageEn')
  return t('settings.languageAuto')
})

const languageError = ref('')
const languageSwitching = ref(false)

async function setLanguagePref(lang: string) {
  if (lang === appConfig.language || languageSwitching.value) return
  languageSwitching.value = true
  languageError.value = ''
  try {
    await applyLanguage(lang)
  } catch {
    languageError.value = t('settings.languageError')
  } finally {
    languageSwitching.value = false
  }
}

// ─── Model download source ───────────────────────────────────────────────────
const downloadSource = computed(() => appConfig.downloadSource)

const sourceOptions = computed(() => [
  { value: 'hf', label: t('settings.sourceHfShort') },
  { value: 'modelscope', label: t('settings.sourceMsShort') },
  { value: 'huggingface', label: t('settings.sourceOfficialShort') },
])

const sourceCurrentLabel = computed(() => {
  if (downloadSource.value === 'modelscope') return t('settings.sourceModelScope')
  if (downloadSource.value === 'huggingface') return t('settings.sourceHuggingFace')
  return t('settings.sourceHf')
})

const sourceError = ref('')
const sourceSwitching = ref(false)

async function setSource(source: string) {
  if (source === appConfig.downloadSource || sourceSwitching.value) return
  sourceSwitching.value = true
  sourceError.value = ''
  try {
    await applyDownloadSource(source)
  } catch {
    sourceError.value = t('settings.sourceError')
  } finally {
    sourceSwitching.value = false
  }
}

// ─── Directories ────────────────────────────────────────────────
// Download paths decide where new llama.cpp installs and model downloads
// land. The backend persists the choice and refreshes its caches; local state
// is updated only after a non-empty pick so a cancelled dialog leaves the
// display unchanged. Errors are silent (the dialog already reports failure).
async function chooseLlamaCppDownloadDir() {
  const dir = await browseLlamaCppDownloadDir()
  if (dir) appConfig.llamaCppDownloadDir = dir
}

async function chooseModelDownloadDir() {
  const dir = await browseModelDownloadDir()
  if (dir) appConfig.modelDownloadDir = dir
}

// OS-scoped gate for the directory browse buttons: Android has no native
// directory picker (the Browse* bindings error there), so on android the path
// rows stay readable but the pick buttons are hidden.
const isAndroid = computed(() => platform.value.isAndroid)

// Android phone model-directory sub (frame ⑯): scanned model count + total
// size ("{n} 个模型 · {size} · 路径由系统管理"). getModels is the cheap cached
// scan; until it resolves (or when it fails) the row keeps the plain
// description.
interface ScannedModel {
  sizeBytes?: number
}
const scannedModels = ref<ScannedModel[] | null>(null)

const modelDirSub = computed(() => {
  if (!(isAndroid.value && isPhone.value) || scannedModels.value === null) {
    return t('settings.modelDownloadDirDesc')
  }
  const totalBytes = scannedModels.value.reduce((sum, m) => sum + (m.sizeBytes || 0), 0)
  return t('settings.modelDirAndroidCounted', { n: scannedModels.value.length, size: formatBytes(totalBytes) })
})

// ─── Server access scope ─────────────────────────────────────────────────────
// (listen address, see backend SaveServerConfig): refreshed from backend on
// mount to stay in sync with persisted values from the API page and other
// sources. Compact segment labels; full names on the sub-line.
const accessOptions = computed(() => [
  { value: 'local', label: t('settings.accessLocalShort') },
  { value: 'lan', label: t('settings.accessLanShort') },
])

const accessError = ref('')
const accessSwitching = ref(false)

async function setAccessScope(mode: string) {
  if (mode === appConfig.serverAccessMode || accessSwitching.value) return
  accessSwitching.value = true
  accessError.value = ''
  try {
    await applyServerAccessMode(mode)
  } catch {
    accessError.value = t('settings.accessError')
  } finally {
    accessSwitching.value = false
  }
}

// Optional llama-server API key (bearer token; empty = no authentication): saved on
// change through setApiKey (whole serverConfig round-trip, same as the access scope).
// The input keeps the user's text on failure so they can fix and retry.
const apiKeyInput = ref('')
const apiKeyError = ref('')
const apiKeySwitching = ref(false)
const showApiKeySheet = ref(false)
const showApiKeyDialog = ref(false)

async function saveApiKey() {
  if (apiKeySwitching.value) return
  apiKeySwitching.value = true
  apiKeyError.value = ''
  try {
    await applyApiKey(apiKeyInput.value)
  } catch {
    apiKeyError.value = t('settings.apiKeyError')
  } finally {
    apiKeySwitching.value = false
  }
}

// Inference GPU selection: pins the serving GPU by stable nvidia-smi UUID
// (backend pins the llama-server child via CUDA_VISIBLE_DEVICES). Options are
// Auto plus one entry per detected GPU (label: name · VRAM · uuid prefix,
// value: full UUID). Saved through the same whole-serverConfig round-trip as
// the access scope / API key; the selection only sticks after a successful
// save so a backend rejection keeps the previous choice visible.
interface GpuEntry {
  name: string
  memoryMb: number
  uuid: string
}
const gpuSnapshot = ref<GpuEntry[] | null>(null)
const gpuValue = ref('')
const gpuError = ref('')
const gpuSwitching = ref(false)

const gpuOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('settings.gpu.auto') },
  ...(gpuSnapshot.value ?? [])
    .filter((g) => g.uuid)
    .map((g) => ({
      value: g.uuid,
      label: `${g.name} · ${formatMB(g.memoryMb)} · ${g.uuid.slice(0, 8)}`,
    })),
])

// A selectable GPU exists only when the probe returned at least one UUID;
// otherwise the selector stays disabled and the no-GPU hint is shown.
const gpuDetected = computed(() => !!gpuSnapshot.value?.some((g) => g.uuid))

async function onGpuSelected(value: string) {
  if (value === gpuValue.value || gpuSwitching.value) return
  gpuSwitching.value = true
  gpuError.value = ''
  try {
    const scfg = await getServerConfig()
    scfg.deviceId = value
    await saveServerConfig(scfg)
    gpuValue.value = value
  } catch {
    gpuError.value = t('settings.gpu.error')
  } finally {
    gpuSwitching.value = false
  }
}

// OS-scoped setting gates: driven by the shared platform state, not by
// viewport tiers. All helpers except tray are Windows-only today; see
// lib/platform.ts for the per-feature rationale.
const showTray = computed(() => showTraySetting(platform.value))
const showApiRoute = computed(() => showApiRouteSetting(platform.value))
const showGpu = computed(() => showServingGpuSetting(platform.value))
const updatesNative = computed(() => updateSectionMode(platform.value) === 'native')
// Whether the update row renders the manual check-for-updates action cluster
// (error / "up to date" / check button). Windows and Android both have an
// in-app install path (NSIS / PackageInstaller); linux/darwin/other short-circuit
// the update probe to "no update" so the action would be a no-op there.
const showCheckActions = computed(() => showUpdateCheckActions(platform.value))

// System tray toggle: rendered on Windows/macOS only. Disabling takes effect immediately (backend removes icon and
// persists); systray cannot restart in same process, so re-enabling requires app restart (hint shown below toggle).
const trayError = ref('')
const traySwitching = ref(false)

async function toggleTray() {
  if (traySwitching.value) return
  traySwitching.value = true
  trayError.value = ''
  try {
    await applyTrayEnabled(!appConfig.trayEnabled)
  } catch {
    trayError.value = t('settings.trayError')
  } finally {
    traySwitching.value = false
  }
}

// API-route mode toggle: rendered on Windows only (backend gates headless to Windows).
// Enabling relaunches the app headless and quits the GUI without stopping llama-server;
// in GUI mode the state is always off, so this effectively only ever enables. On success
// the process quits (no visible state change to roll back); failures surface inline.
// Unavailable under wails dev (vite dev server): the relaunch escapes the wails dev
// supervisor, killing the dev session and leaving the successor without a frontend
// server. import.meta.env.DEV mirrors the backend's `dev` build-tag detection.
const apiRouteDevBlocked = import.meta.env.DEV
const apiRouteError = ref('')
const apiRouteSwitching = ref(false)

async function toggleApiRouteMode() {
  if (apiRouteSwitching.value) return
  if (apiRouteDevBlocked || !appConfig.trayEnabled) return
  apiRouteSwitching.value = true
  apiRouteError.value = ''
  try {
    await setApiRouteMode(!appConfig.apiRouteMode)
  } catch {
    apiRouteError.value = t('settings.apiRouteModeError')
  } finally {
    apiRouteSwitching.value = false
  }
}

const appVersion = ref('')
const checking = computed(() => updateState.checking)
const checkError = computed(() => updateState.error)
const checkResult = computed(() => updateState.result)

// Link mode = every platform without the native check-for-updates action
// (linux/darwin/other: the backend update probe short-circuits to "no update",
// so the action cluster would be a no-op; the row falls back to the hint +
// GitHub Releases link, and Android/Windows render the action cluster above).
const updatesLink = computed(() => !updatesNative.value)

// Phone link mode (frame ⑯): static green "up to date · v{x}" sub — the
// automatic check + UpdateModal stay the actual discovery path. Desktop link
// mode keeps the version-only description.
const updateSub = computed(() =>
  updatesLink.value && isPhone.value
    ? t('settings.updateLatestVersion', { version: appVersion.value || '—' })
    : t('settings.updateDesc', { version: appVersion.value }),
)

onMounted(async () => {
  if (!appConfig.loaded) await loadConfig()
  // Read the current service access scope from the backend (default local) so the page selection matches the persisted value
  getServerConfig().then((scfg) => {
    if (scfg.accessMode === 'local' || scfg.accessMode === 'lan') {
      appConfig.serverAccessMode = scfg.accessMode
    }
    // seed the API key input from the persisted server config (empty = no authentication)
    apiKeyInput.value = scfg.apiKey || ''
    // seed the serving-GPU selection from the persisted server config (empty = auto)
    gpuValue.value = scfg.deviceId || ''
  }).catch(() => {})
  // GPU option list comes from the (cached) system info snapshot; failures
  // leave the selector disabled with the no-GPU hint.
  getSystemInfo()
    .then((info: { gpu?: GpuEntry[] }) => { gpuSnapshot.value = info.gpu ?? [] })
    .catch(() => { gpuSnapshot.value = [] })
  // Acceleration build for the device island: probe failures degrade to the
  // platform capability guess (accelBuildKey) computed in accelLabel.
  getLlamaCpp()
    .then((info: { accel?: string }) => { llamacppAccel.value = info?.accel ?? '' })
    .catch(() => { llamacppAccel.value = '' })
  getAppVersion().then((v) => { appVersion.value = v }).catch(() => {})
  // Android phone directory sub (frame ⑯): cheap cached scan feeding the
  // model count + total size; failures keep the plain description
  getModels()
    .then((list) => { scannedModels.value = list as ScannedModel[] })
    .catch(() => { scannedModels.value = null })
})

async function manualCheck() {
  await checkForUpdate()
}
</script>

<style scoped>
.page {
  /* No top padding: header flush with content top, title aligns with sidebar logo (see global.css .page-header) */
  padding: 0 48px 60px;
}

.page-header {
  /* Use padding instead of margin: header background covers this gap so content scrolls without leaving a seam */
  padding-bottom: 20px;
}

.page-title {
  font-size: 28px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0 0 4px;
  letter-spacing: -0.5px;
  line-height: 1.2;
}

.page-subtitle {
  font-size: 14px;
  color: var(--text-dim);
  margin: 0;
}

/* ─── Docs entry card (v1 mobile design draft frame ⑤) ───
   Brand-gradient hero row linking to the /docs tutorial: white text, 36px
   icon tile, rgba-white sub line and trailing chevron. */
.docs-entry {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 16px 18px;
  margin-bottom: 16px;
  border-radius: var(--r-md, 22px);
  background: var(--grad, linear-gradient(135deg, #6366f1 0%, #8b5cf6 55%, #a855f7 100%));
  color: #fff;
  text-decoration: none;
  box-shadow: none;
  transition: transform 0.15s ease, box-shadow 0.15s ease;
}

.docs-entry:hover {
  color: #fff;
  transform: translateY(-1px);
  box-shadow: none;
}

.docs-entry-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--r-sm);
  background: rgba(255, 255, 255, 0.2);
  flex-shrink: 0;
}

.docs-entry-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.docs-entry-title {
  font-size: 14px;
  font-weight: 800;
  line-height: 1.35;
}

.docs-entry-sub {
  font-size: 11.5px;
  color: rgba(255, 255, 255, 0.75);
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.docs-entry-arrow {
  margin-left: auto;
  color: rgba(255, 255, 255, 0.85);
  flex-shrink: 0;
}

/* ─── Device island (frame ⑤ "设备") ─── */
.device-card {
  background: var(--bg-card);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
  padding: 18px;
  margin-bottom: 16px;
}

.device-label {
  font-size: 12px;
  font-weight: 700;
  color: var(--text-dim);
  margin-bottom: 10px;
}

.device-chips {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.device-chip {
  font-size: 12px;
  font-weight: 700;
  color: var(--text-secondary);
  background: var(--bg-secondary);
  border-radius: 10px;
  padding: 8px 12px;
}

/* ─── Rounded group list (frame ⑤ .group): one floating island per group,
       rows separated by hairlines ─── */
.settings-group {
  background: var(--bg-card);
  border-radius: var(--r-md);
  box-shadow: var(--shadow-island);
  margin-bottom: 16px;
  overflow: hidden;
}

.group-item {
  padding: 6px 18px;
}

.group-item + .group-item {
  border-top: 1px solid var(--border-light);
}

.group-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 9px 0;
}

/* 36px colored icon brick (frame ⑤ .row .ic) */
.row-ic {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--r-sm);
  flex-shrink: 0;
}

/* Per-row accent tints: translucent hues read on both light and dark cards */
.ic-indigo {
  background: rgba(99, 102, 241, 0.14);
  color: #6366f1;
}

.ic-emerald {
  background: rgba(16, 185, 129, 0.14);
  color: var(--success);
}

.ic-amber {
  background: rgba(245, 158, 11, 0.16);
  color: var(--warning);
}

.ic-violet {
  background: rgba(139, 92, 246, 0.14);
  color: #8b5cf6;
}

.ic-sky {
  background: rgba(14, 165, 233, 0.14);
  color: #0ea5e9;
}

.ic-rose {
  background: rgba(244, 63, 94, 0.12);
  color: #f43f5e;
}

.ic-teal {
  background: rgba(20, 184, 166, 0.14);
  color: #14b8a6;
}

.ic-slate {
  background: rgba(100, 116, 139, 0.16);
  color: #64748b;
}

.ic-fuchsia {
  background: rgba(168, 85, 247, 0.14);
  color: #a855f7;
}

.ic-blue {
  background: rgba(59, 130, 246, 0.14);
  color: #3b82f6;
}

/* Main / sub two-line copy (frame ⑤ .row main + .sub) */
.row-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.row-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  line-height: 1.35;
}

.row-sub {
  font-size: 11.5px;
  color: var(--text-dim);
  font-weight: 500;
  margin-top: 3px;
}

/* Green "up to date" sub (frame ⑯ .sub.ok, phone release-link row only) */
.row-sub-ok {
  color: var(--success);
  font-weight: 700;
}

.row-tail {
  margin-left: auto;
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

/* API-key row tail (the "设置 ›" dialog trigger): base recipe mirrors the
   .api-key-dialog-cancel button (13px/600 accent text, hairline border,
   subtle fill + glow hover); the phone block below only overrides the
   frame-⑯ compact touch skin and the 44px touch target */
.row-tail-api-key {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  width: auto;
  padding: 7px 14px;
  background: var(--active-bg);
  border: 1px solid var(--overlay-20);
  border-radius: 8px;
  color: var(--accent-light);
  font-size: 13px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: all 0.2s;
}

.row-tail-api-key:hover {
  background: var(--accent-glow);
}

/* Footnote / error lines under a row */
.row-foot {
  font-size: 11.5px;
  color: var(--text-dim);
  margin: 0;
  padding: 0 0 9px 50px;
}

.row-error {
  margin: 0;
  padding: 0 0 9px 50px;
  font-size: 12px;
  color: #ef4444;
}

/* ─── Gradient capsule switch (frame ⑤ .sw): gradient = on ─── */
.switch {
  width: 46px;
  height: 27px;
  border-radius: 999px;
  background: var(--overlay-20);
  position: relative;
  cursor: pointer;
  user-select: none;
  transition: background 0.25s ease;
  flex-shrink: 0;
}

.switch::after {
  content: "";
  position: absolute;
  width: 23px;
  height: 23px;
  border-radius: 50%;
  background: #fff;
  top: 2px;
  left: 2px;
  box-shadow: none;
  transition: transform 0.25s ease;
}

.switch.on {
  background: var(--grad, linear-gradient(135deg, #6366f1 0%, #8b5cf6 55%, #a855f7 100%));
}

.switch.on::after {
  transform: translateX(19px);
}

.switch.disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

/* ─── Compact segmented control in a row tail (models-seg pattern) ─── */
.row-seg {
  display: flex;
  padding: 3px;
  background: rgba(120, 124, 160, 0.12);
  border-radius: 999px;
}

.row-seg-btn {
  padding: 6px 12px;
  background: none;
  border: none;
  border-radius: 999px;
  color: var(--text-muted);
  font-size: 12px;
  font-weight: 700;
  font-family: inherit;
  cursor: pointer;
  transition: color 0.15s, background 0.2s, box-shadow 0.2s;
  white-space: nowrap;
}

.row-seg-btn:hover {
  color: var(--text-secondary);
}

.row-seg-btn.active {
  background: var(--bg-secondary);
  color: var(--text-primary);
  box-shadow: none;
}

/* ─── API key input (row tail) ─── */
.api-key-input {
  width: 240px;
  padding: 8px 12px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-primary);
  font-size: 13px;
  font-family: var(--font-mono);
  outline: none;
  transition: border-color 0.15s;
  flex-shrink: 0;
}

.api-key-input:focus {
  border-color: var(--accent);
}

.api-key-input:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Inference GPU selector: fixed-width trigger so the row label keeps its width */
.gpu-select {
  width: 340px;
  flex-shrink: 0;
}

/* ─── Directories ─── */
/* Full path on its own line under the label/desc, button vertically centered
   beside it (same row), so long Windows paths stay fully readable */
.dir-path-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 4px 0 10px 50px;
}

.dir-path {
  flex: 1;
  min-width: 0;
  word-break: break-all;
  line-height: 1.4;
  font-size: 12px;
  font-family: var(--font-mono);
  color: var(--text-muted);
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 5px 10px;
}

.dir-btn {
  padding: 6px 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
}

.dir-btn:hover {
  background: var(--hover-bg);
  color: var(--text-primary);
}

/* ─── Updates ─── */
.update-actions {
  gap: 12px;
}

/* Non-Windows updates hint (link mode): full-width footnote under the row */
.update-hint {
  text-align: left;
}

.hint-link {
  color: var(--accent);
  text-decoration: none;
  white-space: nowrap;
}

.hint-link:hover {
  text-decoration: underline;
}

.update-latest {
  font-size: 13px;
  font-weight: 600;
  color: var(--success);
  white-space: nowrap;
}

.btn-check {
  padding: 8px 20px;
  background: var(--accent);
  border: none;
  border-radius: 8px;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.15s;
  white-space: nowrap;
}

.btn-check:hover:not(:disabled) {
  opacity: 0.85;
}

.btn-check:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* The native update action's inline error sits inside the tail: no foot indent */
.update-actions .row-error {
  padding: 0;
}

/* ─── About rows (inside the about group) ─── */
.about-row {
  display: flex;
  align-items: baseline;
  gap: 16px;
  padding: 8px 0;
}

.about-row + .about-row {
  border-top: 1px solid var(--border-light);
}

.about-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
  min-width: 72px;
  flex-shrink: 0;
}

.about-value {
  font-size: 13px;
  color: var(--text-muted);
}

/* Repository URL in monospace, kept selectable (user-select: text) so it can
   be copied; not an <a> to avoid navigating the WebView away */
.about-mono {
  font-family: var(--font-mono);
  word-break: break-all;
  user-select: text;
}

/* ─── Desktop two-column layout: 1280px+ (design draft D5) ─── */
@media (min-width: 1280px) {
  .page {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 20px;
    max-width: 1280px;
    margin-left: auto;
    margin-right: auto;
  }

  .sticky-top,
  .docs-entry,
  .device-card {
    grid-column: 1 / -1;
  }

  .settings-group {
    margin-bottom: 0;
  }
}

/* ─── Phone (<=767px): rows keep their island cards but every control becomes
       thumb-friendly — tails wrap under the labels at full width, segments and
       inputs go full-width, and long directory paths wrap. ─── */
@media (max-width: 767px) {
  .page {
    padding: 0 18px 60px;
  }

  /* Phone heading = the design's 24px phone tier (same as Home's .greet-title
     phone rule, same 1.2 line-height), so every page header block reads the
     same height as the greeting */
  .page-title {
    font-size: 24px;
  }

  .group-row {
    flex-wrap: wrap;
    gap: 10px;
  }

  /* The text column takes the space beside the brick and wraps internally,
     so a long sub line cannot push the whole column under the icon */
  .row-text {
    flex: 1 1 120px;
  }

  .row-tail {
    width: 100%;
    justify-content: flex-end;
  }

  /* Switch rows keep the control beside the label: the capsule is compact
     enough to fit the line, so it stays vertically centered against the
     label block (group-row align-items: center) instead of dropping to the
     row's bottom edge like the full-width tails below. Only wide controls
     (segments / inputs / buttons) need the wrap-to-full-width treatment. */
  .row-tail.row-tail-switch {
    width: auto;
  }

  /* Controls drop under their labels at full width */
  .row-seg {
    width: 100%;
  }

  .row-seg-btn {
    flex: 1;
    min-height: 44px;
  }

  .gpu-select,
  .api-key-input {
    width: 100%;
    flex-shrink: 1;
  }

  .gpu-select {
    margin-left: 0;
  }

  .gpu-select :deep(.themed-select__trigger) {
    min-height: 44px;
  }

  .api-key-input {
    min-height: 44px;
  }

  /* The base 46×27 capsule already matches the mockup: the former phone
     enlargement is intentionally NOT re-applied here (frame ⑯ .sw) */

  .dir-path-row {
    flex-wrap: wrap;
    padding-left: 0;
  }

  .dir-btn {
    min-height: 44px;
    padding: 10px 16px;
  }

  .row-foot,
  .row-error {
    padding-left: 0;
  }

  .update-actions {
    flex-wrap: wrap;
  }

  .btn-check {
    min-height: 44px;
  }

  /* ─── Frame ⑯ phone styling ─── */

  /* Docs entry card: 18px padding, stronger brand shadow, 15/700 title,
     brighter sub, and the plain → glyph tail instead of the chevron SVG */
  .docs-entry {
    padding: 18px;
    box-shadow: none;
  }

  .docs-entry-title {
    font-size: 15px;
    font-weight: 700;
  }

  .docs-entry-sub {
    color: rgba(255, 255, 255, 0.8);
  }

  .docs-entry-arrow {
    display: none;
  }

  .docs-entry-arrow-glyph {
    margin-left: auto;
    color: rgba(255, 255, 255, 0.85);
    font-size: 16px;
    font-weight: 700;
    flex-shrink: 0;
  }

  /* Device island chips sit on the shared second surface */
  .device-chip {
    background: var(--surface-2);
  }

  /* Icon bricks (frame ⑯ .row .ic): 34px pastel solid tiles; the dark theme
     flattens every brick to one muted surface with a single violet glyph
     tone (mockup dark rail) */
  .row-ic {
    width: 34px;
    height: 34px;
    border-radius: var(--r-sm);
  }

  .ic-indigo {
    background: #eef0ff;
    color: #4f46e5;
  }

  .ic-emerald {
    background: #ecfdf3;
    color: #059669;
  }

  .ic-amber {
    background: #fff7ea;
    color: #b45309;
  }

  .ic-violet {
    background: #f3e8ff;
    color: #9333ea;
  }

  .ic-sky {
    background: #e0f2fe;
    color: #0369a1;
  }

  .ic-rose {
    background: #f1f5f9;
    color: #475569;
  }

  .ic-teal {
    background: #f0fdfa;
    color: #0f766e;
  }

  .ic-slate {
    background: #f1f5f9;
    color: #475569;
  }

  .ic-fuchsia {
    background: #fdf4ff;
    color: #a21caf;
  }

  .ic-blue {
    background: #fdecec;
    color: #dc2626;
  }

  .ic-about {
    background: #f8fafc;
    color: #475569;
  }

  html[data-theme='dark'] .row-ic {
    background: var(--surface-2);
    color: var(--accent-light);
  }

  .row-title {
    font-size: 13px;
  }

  /* Frame ⑯ link tails: compact row tail (the phone .row-tail below goes
     full-width — these two stay beside the label) with a 44px hit target */
  .row-tail.updates-link,
  .row-tail.about-link {
    width: auto;
    min-height: 44px;
    gap: 6px;
    color: var(--text-secondary);
    text-decoration: none;
    font-size: 13px;
    font-weight: 600;
    white-space: nowrap;
  }

  /* Frame ⑯ select tails: compact inline select beside the label (same
     auto-width policy as the switch tails above) */
  .row-tail-select {
    width: auto;
    min-height: 44px;
  }

  /* Compact ThemedSelect trigger inside a select tail: shrink to the value
     text + chevron, no extra padding */
  .row-tail-select :deep(.themed-select__trigger) {
    padding: 6px 10px;
    font-size: 12px;
    font-weight: 600;
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: 10px;
    color: var(--text-secondary);
    min-height: 36px;
  }

  /* Phone row-tail menus are fixed bottom sheets (frame ⑯). The menu is
     teleported to <body> and marked via the ThemedSelect menu-class prop — a
     teleported node is no longer a descendant of .row-tail-select, so the
     former :deep() rule cannot reach it. The !important flags are required to
     beat the component's inline fixed-position style (left/width/top computed
     from the trigger rect). */
  :global(.settings-sheet-menu) {
    position: fixed;
    left: 10px !important;
    right: 10px;
    top: auto !important;
    bottom: calc(var(--mobile-nav-height, 0px) + 10px + var(--keyboard-inset, 0px)) !important;
    width: auto !important;
    max-height: 50vh;
    border-radius: var(--r-lg);
    border: none;
    box-shadow: 0 24px 60px rgba(15, 17, 28, 0.40);
  }

  /* API key row tail (frame ⑯): the shared button recipe lives at the top
     level; only the compact touch skin + 44px touch target differ here */
  .row-tail-api-key {
    min-height: 44px;
    padding: 6px 12px;
    background: var(--surface-2);
    border-color: var(--border);
    border-radius: 10px;
    color: var(--text-secondary);
    font-size: 12px;
  }

  /* API key bottom sheet (frame ⑯) */
  .api-key-dim {
    display: block;
    position: fixed;
    inset: 0;
    z-index: 39;
    background: rgba(16, 18, 33, 0.42);
  }

  .api-key-sheet {
    position: fixed;
    left: 10px;
    right: 10px;
    top: auto;
    bottom: calc(var(--mobile-nav-height, 0px) + 10px + var(--keyboard-inset, 0px));
    z-index: 40;
    background: var(--bg-secondary);
    border: none;
    border-radius: 26px;
    box-shadow: none;
    padding: 18px 20px 16px;
  }

  .api-key-grab {
    display: block;
    width: 40px;
    height: 4px;
    border-radius: 999px;
    background: var(--border);
    margin: 0 auto 12px;
  }

  .api-key-sheet-title {
    font-size: 17px;
    font-weight: 800;
    color: var(--text-primary);
    margin-bottom: 12px;
  }

  .api-key-sheet-input {
    width: 100%;
    padding: 12px 14px;
    background: var(--surface-2);
    border: none;
    border-radius: var(--r-sm);
    color: var(--text-primary);
    font-size: 14px;
    font-family: var(--font-mono);
    outline: none;
    margin-bottom: 10px;
    box-sizing: border-box;
  }

  .api-key-done {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    min-height: 44px;
    padding: 13px 0;
    border: none;
    border-radius: var(--r-md);
    background: var(--grad);
    color: #fff;
    font-size: 14px;
    font-weight: 800;
    font-family: inherit;
    cursor: pointer;
  }
}

/* Desktop API key dialog (design draft F5): centered modal */
.api-key-dialog-root {
  position: fixed;
  inset: 0;
  z-index: 39;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(16, 18, 33, 0.42);
}

.api-key-dialog {
  /* Glass modal (design draft v2 rule 2) */
  background: var(--glass);
  backdrop-filter: blur(22px) saturate(1.6);
  -webkit-backdrop-filter: blur(22px) saturate(1.6);
  border: 1px solid var(--glass-line);
  border-radius: var(--r-lg);
  box-shadow: 0 24px 60px rgba(15, 17, 28, 0.40);
  padding: 20px;
  width: 340px;
  max-width: 90vw;
}

.api-key-dialog-title {
  font-size: 14px;
  font-weight: 700;
  color: var(--text-primary);
  margin-bottom: 12px;
}

.api-key-dialog-input {
  width: 100%;
  padding: 10px 12px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--text-primary);
  font-size: 13px;
  font-family: var(--font-mono);
  outline: none;
  margin-bottom: 12px;
  box-sizing: border-box;
}

.api-key-dialog-input:focus {
  border-color: var(--accent);
}

.api-key-dialog-actions {
  display: flex;
  justify-content: flex-end;
}

.api-key-dialog-cancel {
  padding: 7px 16px;
  background: var(--active-bg);
  color: var(--accent-light);
  border: 1px solid var(--overlay-20);
  border-radius: 8px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
}

.api-key-dialog-cancel:hover {
  background: var(--accent-glow);
}

/* ─── Tablet portrait Track A (768–1099px, draft frame ⑯/A16 adapted): a
       two-column group grid — the landscape B16 concept brought down to
       portrait proportions: appearance group (theme / language / download
       source) LEFT, directories/service + about groups RIGHT, the device
       island full-width on top, and the help-and-tutorial entry card closing
       the page as a full-width row at the bottom. The entry card is restyled
       from the brand-gradient phone hero into the draft's surface row card
       (.group + grad-soft icon tile + ink text). The existing group classes
       (group-appearance / group-service / group-about) are reused via
       band-scoped rules — no DOM duplication. Scoped to the band with
       min-width: 768px so phones (<=767) and desktop (>=1100px) keep the hero
       at the top. ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  .page {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
    align-items: start;
  }

  /* Header and the device island ride the full width above the group columns */
  .sticky-top,
  .device-card {
    grid-column: 1 / -1;
  }

  /* Landscape B16 column placement: appearance LEFT, service + about RIGHT */
  .group-appearance {
    grid-column: 1;
  }

  .group-service,
  .group-about {
    grid-column: 2;
  }

  /* Grid gap replaces the flow margins (same policy as the >=1280px desktop
     grid) */
  .device-card,
  .settings-group,
  .docs-entry {
    margin-bottom: 0;
  }

  /* order moves the entry after every order-0 child (header / device card /
     setting groups) — the draft's closing entry card, spanning the full width */
  .docs-entry {
    order: 1;
    grid-column: 1 / -1;
    background: var(--bg-card);
    box-shadow: var(--shadow-island);
    color: var(--text-primary);
  }

  .docs-entry:hover {
    color: var(--text-primary);
    transform: none;
    box-shadow: var(--shadow-island);
  }

  /* Draft A16 icon brick: grad-soft tile + violet glyph on the surface card */
  .docs-entry-icon {
    background: var(--grad-soft);
    color: var(--accent-light);
  }

  .docs-entry-sub {
    color: var(--text-dim);
  }

  .docs-entry-arrow {
    color: var(--text-dim);
  }

  /* The serving-GPU selector's desktop 340px fixed width would overflow the
     ~half-page column: let it shrink inside the row instead */
  .gpu-select {
    flex-shrink: 1;
    min-width: 0;
  }

  /* Long tails (the update row's "up to date" + check-button pair) would
     crush the label to one character per line inside the ~half-page columns:
     let the row wrap and drop the tail below the label — the same recipe as
     the phone tier, applied only where an inline tail does not fit */
  .group-row {
    flex-wrap: wrap;
  }

  .row-text {
    flex: 1 1 120px;
  }

  /* The toolbar select's 240px min-width crushes the row label to one
     character per line inside the ~half-page columns: switch the row-tail
     selects to the compact inline trigger (same treatment as the phone
     tier), so the label column keeps a readable width */
  .row-tail-select {
    min-height: 44px;
  }

  .row-tail-select :deep(.themed-select__trigger) {
    min-width: 0;
    padding: 6px 10px;
    font-size: 12px;
    font-weight: 600;
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: 10px;
    color: var(--text-secondary);
    min-height: 36px;
  }
}

</style>
