<template>
  <div
    class="chat-page"
    :class="{
      'chat-page--dock-left': laneLeftActive,
      'chat-page--dock-right': laneRightActive,
    }"
  >
    <!-- Lane mirroring, three-state (dockLane from lib/dockSpace), DESKTOP
         ONLY: the measured reserve lane exists only while the capsule actually
         rides in the composer band at the bottom of the window — 'left' moves
         the lane to the LEFT padding, 'right' keeps the original right lane,
         and 'none' (capsule parked mid-screen / up top, or dock hidden:
         dockWidth = 0) retracts the lane so the composer spans the base
         gutters. The phone tier never narrows the page for the capsule (the
         composer stays full-width; TaskDock keeps the capsule clear of the
         composer itself by clamping). The docked-in-band default reproduces
         the historical desktop layout byte-for-byte.
         WARNING: this comment MUST stay INSIDE the root div. A top-level
         comment makes the component multi-root (dev builds keep comment
         vnodes), and a fragment-rooted child under App.vue's out-in
         <transition> loses the leave hook handoff: state.isLeaving sticks
         true and every navigation after visiting Chat renders an empty
         placeholder until a full reload. viewsSingleRoot.test.ts guards
         this for all route views. -->
    <div class="sticky-top">
      <!-- Design frame ②: the header slims down to a model capsule chip (the
           gradient dot is the brand/status mark, the name + chevron open the
           model picker). The big page title is gone on all tiers — the sidebar
           / bottom nav already carry the page identity and the phone viewport
           belongs to the conversation. -->
      <div class="chat-toolbar">
        <!-- Model picker: themed dropdown (popup list is rendered in-app, so it
             follows the theme — a native select's popup is OS-rendered).
             Options come from the local model scan, so picking works with the
             service stopped; sending then auto-starts it. The chip look is a
             scoped :deep() reskin of the toolbar variant; the trigger stays a
             real button with the full WAI-ARIA select-only combobox wiring. -->
        <ThemedSelect
          variant="toolbar"
          class="chat-model-select"
          :class="{ 'chat-model-select--empty': modelOptions.length === 0, 'chat-model-select--down': chipDotDown }"
          :model-value="selectedModel"
          :options="modelOptions"
          :disabled="serviceStarting || streaming"
          :placeholder="chipPlaceholder"
          :label="t('chat.model')"
          :empty-text="t('chat.noModels')"
          @update:model-value="pickModel"
        />
        <!-- Generation preset picker (borrowed from unsloth's generation-presets):
             quick sampling profiles. "默认" sends NO sampling fields (llama-server's
             saved per-model params apply); any other preset attaches
             temperature/top_p/top_k/repeat_penalty to the request body. Same
             ThemedSelect toolbar variant as the model chip so both tiers narrow
             identically; the bookmark button opens the save-as / delete dialog. -->
        <ThemedSelect
          variant="toolbar"
          class="chat-preset-select"
          :model-value="genPresetId"
          :options="genPresetOptions"
          :disabled="streaming"
          :label="t('chat.genPreset')"
          @update:model-value="pickGenPreset"
        />
        <button
          class="chat-icon-btn chat-preset-manage-btn"
          @click.stop="openPresetDialog"
          :aria-label="t('chat.presetManage')"
          :title="t('chat.presetManage')"
          type="button"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z"/>
          </svg>
        </button>
        <button
          class="chat-icon-btn chat-clear-btn"
          @click="clearChat"
          :disabled="messages.length === 0 || streaming"
          :aria-label="t('chat.clear')"
          :title="t('chat.clear')"
          type="button"
        >
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 6h18"/>
            <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/>
            <path d="M10 11v6M14 11v6"/>
          </svg>
        </button>
        <button
          class="chat-icon-btn chat-settings-btn"
          @click.stop="showParams = !showParams"
          :aria-expanded="showParams"
          :aria-label="t('chat.settings')"
          :title="t('chat.settings')"
          type="button"
        >
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
          </svg>
        </button>

        <!-- Chat parameters panel: desktop keeps the anchored popover; the phone
             tier (frame ⑤) gets a bottom sheet and the tablet-portrait tier a
             centered modal card — both rendered by the Teleport below. The
             paramsLayout gate (not a bare !isMobileTier) also keeps the popover
             from reappearing if showParams is left open across a tier switch
             (e.g. resizing a desktop window into the tablet band). -->
        <div v-if="showParams && paramsLayout === 'popover'" class="params-popover" @click.stop>
          <div class="params-header">{{ t('chat.settings') }}</div>
          <!-- Sampling fields are owned by the generation preset (toolbar picker):
               the "默认" preset sends no sampling fields (llama-server's saved
               per-model params apply), any other preset sends its four values.
               The panel shows the active preset read-only instead of inputs. -->
          <div class="params-row params-row-full preset-summary">
            <span class="preset-summary-label">{{ t('chat.genPreset') }}</span>
            <span class="preset-summary-value">{{ currentGenPresetLabel }}<template v-if="presetSummaryText"> · {{ presetSummaryText }}</template></span>
          </div>
          <div class="params-row">
            <label class="params-label" for="chat-maxtok">{{ t('chat.maxTokens') }}</label>
            <input id="chat-maxtok" class="params-input" type="number" min="-1" v-model.number="chatParams.maxTokens" />
          </div>
          <div class="params-row params-row-full">
            <label class="params-label" for="chat-sys">{{ t('chat.systemPrompt') }}</label>
            <textarea id="chat-sys" class="params-textarea" rows="2" v-model="chatParams.systemPrompt" :placeholder="t('chat.systemPromptPh')"></textarea>
          </div>
          <div class="params-footer">
            <button class="params-reset-btn" @click="resetParams">{{ t('chat.resetDefaults') }}</button>
          </div>
        </div>

        <!-- Phone params sheet (design frame ⑤) / tablet-portrait modal card
             (tablet draft frame ⑤): dim + overlay with slider / stepper
             controls. Same chatParams state and persistence as the popover —
             only the controls and the overlay geometry differ (phone: docked
             full-width bottom sheet; tablet portrait: centered 560px card,
             styled per tier in CSS). .stop keeps in-overlay taps from hitting
             the document-level close handler; tapping the dim closes.

             WARNING: this Teleport MUST be conditionally rendered with
             v-if="overlayParams" (phone sheet + tablet modal tiers only). A
             persistent empty Teleport node in the vnode tree breaks Vue's
             out-in <transition> afterLeave callback on this page:
             BaseTransition waits for the leaving component's afterLeave, but
             the lingering Teleport causes the leave hook to be lost,
             state.isLeaving sticks permanently, and the content area stays
             blank after navigating away from Chat. This is a known
             Vue×Teleport edge case (see vuejs/core#5836 and related issues).
             Desktop never mounts this Teleport node at all; only the
             sheet/modal tiers teleport their params overlay to <body>. -->
        <Teleport v-if="overlayParams" to="body">
          <div v-if="showParams && overlayParams" class="params-sheet-root">
            <div class="params-dim" @click="showParams = false"></div>
            <div
              class="params-sheet"
              :class="{ 'params-sheet--modal': paramsLayout === 'modal' }"
              role="dialog"
              aria-modal="true"
              :aria-label="t('chat.paramsTitle')"
              @click.stop
            >
              <div class="params-grab" aria-hidden="true"></div>
              <div class="params-sheet-head">
                <span class="params-sheet-title">{{ t('chat.paramsTitle') }}</span>
                <button class="params-reset-link" type="button" @click="resetParams">{{ t('chat.resetDefaults') }}</button>
              </div>
              <div class="params-sheet-body">
                <!-- Sampling fields are owned by the generation preset (toolbar
                     picker): read-only summary here instead of sliders/steppers. -->
                <div class="psheet-row psheet-row--text preset-summary">
                  <div class="psheet-label">
                    <span class="psheet-name">{{ t('chat.genPreset') }}</span>
                    <span class="psheet-sub">{{ t('chat.presetSummaryHint') }}</span>
                  </div>
                  <span class="preset-summary-value">{{ currentGenPresetLabel }}<template v-if="presetSummaryText"> · {{ presetSummaryText }}</template></span>
                </div>
                <div class="psheet-row">
                  <div class="psheet-label">
                    <span class="psheet-name">{{ t('chat.maxTokens') }}</span>
                    <span class="psheet-sub">{{ t('chat.maxTokensHint') }}</span>
                  </div>
                  <div class="pstepper">
                    <button class="pstep-btn" type="button" :aria-label="t('chat.stepDown')" @click="chatParams.maxTokens = applyMaxTokensStep(-1)">−</button>
                    <span class="pstep-val">{{ isUnlimitedMaxTokens(chatParams.maxTokens) ? t('chat.unlimited') : chatParams.maxTokens }}</span>
                    <button class="pstep-btn" type="button" :aria-label="t('chat.stepUp')" @click="chatParams.maxTokens = applyMaxTokensStep(1)">+</button>
                  </div>
                </div>
                <div class="psheet-row psheet-row--text">
                  <div class="psheet-label">
                    <span class="psheet-name">{{ t('chat.systemPrompt') }}</span>
                    <span class="psheet-sub">{{ t('chat.systemPromptHint') }}</span>
                  </div>
                  <textarea
                    class="ptext"
                    rows="2"
                    v-model="chatParams.systemPrompt"
                    :placeholder="t('chat.systemPromptPh')"
                    :aria-label="t('chat.systemPrompt')"
                  ></textarea>
                </div>
              </div>
            </div>
          </div>
        </Teleport>

        <!-- Generation preset manager: save-as (captures the active preset's
             sampling values under a new name) + delete custom presets. Rendered
             in-app as a centered card on every tier (theme tokens, mobile-friendly
             width). Conditionally rendered v-if per the Teleport warning above. -->
        <Teleport v-if="genPresetDialogOpen" to="body">
          <div class="preset-dialog-root">
            <div class="preset-dim" @click="closePresetDialog"></div>
            <div class="preset-dialog" role="dialog" aria-modal="true" :aria-label="t('chat.presetManage')" @click.stop>
              <div class="preset-dialog-title">{{ t('chat.presetManage') }}</div>
              <div class="preset-dialog-current">
                <span class="preset-dialog-label">{{ t('chat.genPreset') }}</span>
                <span class="preset-summary-value">{{ currentGenPresetLabel }}<template v-if="presetSummaryText"> · {{ presetSummaryText }}</template></span>
              </div>
              <template v-if="currentGenPreset.sampling && draftSampling">
                <label class="preset-dialog-row">
                  <span class="preset-dialog-label">{{ t('chat.presetSaveAs') }}</span>
                  <input
                    v-model="genPresetSaveName"
                    class="preset-dialog-input"
                    type="text"
                    maxlength="40"
                    :placeholder="t('chat.presetNamePh')"
                    :aria-label="t('chat.presetSaveAs')"
                    @keyup.enter="saveAsGenPreset"
                  />
                </label>
                <!-- Editable sampling draft: initialized from the active preset,
                     clamped on change (lib/genPresets bounds). The builtin
                     presets themselves stay read-only — editing a value only
                     affects the SAVED CUSTOM copy, never the builtin. -->
                <div class="preset-dialog-sampling">
                  <span class="preset-dialog-label">{{ t('chat.presetSamplingEdit') }}</span>
                  <div class="preset-dialog-sampling-grid">
                    <label class="preset-dialog-sfield">
                      <span class="preset-dialog-slabel">{{ t('chat.temperature') }}</span>
                      <input
                        class="preset-dialog-sinput"
                        type="number"
                        min="0" max="2" step="0.05"
                        :value="draftSampling.temperature"
                        :aria-label="t('chat.temperature')"
                        @change="onDraftField('temperature', $event)"
                      />
                    </label>
                    <label class="preset-dialog-sfield">
                      <span class="preset-dialog-slabel">{{ t('chat.topP') }}</span>
                      <input
                        class="preset-dialog-sinput"
                        type="number"
                        min="0" max="1" step="0.01"
                        :value="draftSampling.topP"
                        :aria-label="t('chat.topP')"
                        @change="onDraftField('topP', $event)"
                      />
                    </label>
                    <label class="preset-dialog-sfield">
                      <span class="preset-dialog-slabel">{{ t('chat.topK') }}</span>
                      <input
                        class="preset-dialog-sinput"
                        type="number"
                        min="0" max="200" step="1"
                        :value="draftSampling.topK"
                        :aria-label="t('chat.topK')"
                        @change="onDraftField('topK', $event)"
                      />
                    </label>
                    <label class="preset-dialog-sfield">
                      <span class="preset-dialog-slabel">{{ t('chat.repeatPenalty') }}</span>
                      <input
                        class="preset-dialog-sinput"
                        type="number"
                        min="0.5" max="2" step="0.01"
                        :value="draftSampling.repeatPenalty"
                        :aria-label="t('chat.repeatPenalty')"
                        @change="onDraftField('repeatPenalty', $event)"
                      />
                    </label>
                  </div>
                </div>
                <p v-if="genPresetError" class="preset-dialog-err">{{ genPresetError }}</p>
              </template>
              <div v-if="customGenPresets.length > 0" class="preset-dialog-custom">
                <div class="preset-dialog-label">{{ t('chat.presetCustomList') }}</div>
                <div v-for="p in customGenPresets" :key="p.id" class="preset-dialog-item">
                  <span class="preset-dialog-item-name">{{ p.name }}</span>
                  <button class="preset-dialog-del" type="button" @click="deleteGenPreset(p.id)">{{ t('chat.presetDelete') }}</button>
                </div>
              </div>
              <div class="preset-dialog-actions">
                <button class="preset-dialog-btn" type="button" @click="closePresetDialog">{{ t('chat.presetClose') }}</button>
                <button
                  v-if="currentGenPreset.sampling"
                  class="preset-dialog-btn preset-dialog-btn--primary"
                  type="button"
                  :disabled="!canSaveGenPreset"
                  @click="saveAsGenPreset"
                >{{ t('chat.presetSave') }}</button>
              </div>
            </div>
          </div>
        </Teleport>
      </div>
    </div>

    <!-- Conversation body: messages column. The wrapper is a pure flex
         pass-through (column, flex:1), so the desktop/phone layout chain
         (.chat-page → messages → input) is unchanged. -->
    <div class="chat-body">
      <!-- Messages area -->
      <!-- Delegated link handler: links in assistant markdown open in the system
           browser, the WebView never navigates (see lib/linkHandler.ts) -->
      <div
        ref="messagesContainer"
        class="messages-area"
        :class="{
          'messages-area--streaming': streaming,
          'messages-area--blocked': isTabletTier && composerBlocked,
        }"
        @click="handleLinkClick"
      >
        <!-- Tablet precheck banner (tablet draft frames A⑦/B⑦): the send
             precheck / auto-start notices render INLINE at the top of the
             conversation on tablet tiers — repair entry and status on one
             screen, history dimmed below (.messages-area--blocked) — instead
             of floating above the composer. Same notices array drives both
             placements; phone/desktop keep the input-area stack. -->
        <div v-if="isTabletTier && activeNotices.length" class="start-notice-stack chat-precheck-stack" role="status">
          <div
            v-for="notice in activeNotices"
            :key="notice.kind"
            class="start-notice"
            :class="{ 'start-notice--error': notice.kind === 'error' }"
          >
            <template v-if="notice.kind === 'error'">
              <span class="start-notice-text">{{ notice.text }}</span>
              <button v-if="notice.cause === 'needModels'" class="start-notice-btn" @click="goDownloads">
                {{ t('action.gotoDownloads') }}
              </button>
              <button v-else-if="notice.cause === 'needRuntime'" class="start-notice-btn" @click="goRuntime">
                {{ t('chat.goRuntime') }}
              </button>
            </template>
            <template v-else>
              <span v-if="notice.kind === 'starting'" class="start-notice-spinner" aria-hidden="true"></span>
              <span>{{ notice.text }}</span>
            </template>
          </div>
        </div>
      <!-- Empty states (design frames ⑤⑦ .emptystate): two-line structure with
           an emoji mark on the phone tier; the desktop keeps the original
           single-line text (icon + sub-line are display:none there). -->
      <div v-if="modelOptions.length === 0" class="empty-hint">
        <span class="empty-ico" aria-hidden="true">📦</span>
        <b class="empty-title">{{ isMobileTier ? t('chat.noModelsTitle') : t('chat.noModels') }}</b>
        <span class="empty-sub">{{ t('chat.noModelsSub') }}</span>
      </div>
      <template v-else>
        <div v-if="messages.length === 0" class="empty-hint">
          <span class="empty-ico" aria-hidden="true">💬</span>
          <b class="empty-title">{{ t('chat.emptyHint') }}</b>
          <span class="empty-sub">{{ t('chat.emptySub') }}</span>
        </div>
        <div
          v-for="(msg, idx) in messages"
          :key="idx"
          class="message-row"
          :class="msg.role === 'user' ? 'is-user' : 'is-assistant'"
        >
          <div class="message-bubble">
            <!-- Design frame ②: only assistant bubbles carry a small header —
                 the answering model's display name (user bubbles are identified
                 by position + the gradient skin) -->
            <span v-if="msg.role === 'assistant'" class="message-role">{{ assistantLabel }}</span>
            <!-- Reasoning (thinking) block, assistant messages with thinking output only -->
            <div v-if="msg.reasoning" class="reasoning-block" :class="{ expanded: isReasoningExpanded(idx, msg) }">
              <button class="reasoning-header" type="button" @click="toggleReasoning(idx)">
                <span>{{ thinkingLabel(idx, msg) }}</span>
                <svg class="reasoning-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="6 9 12 15 18 9"/>
                </svg>
              </button>
              <!-- Ref/scroll wiring is bound to the last message only: that is the
                   streaming stick-to-bottom target (see setReasoningBodyRef) -->
              <div
                v-if="isReasoningExpanded(idx, msg)"
                class="reasoning-body"
                :ref="idx === messages.length - 1 ? setReasoningBodyRef : undefined"
                @scroll="onReasoningScroll"
              >{{ msg.reasoning }}</div>
            </div>
            <!-- Images (attached to user messages) -->
            <div v-if="msg.images && msg.images.length" class="message-images">
              <img v-for="(img, i) in msg.images" :key="i" :src="img" class="message-image" alt="" />
            </div>
            <!-- Assistant output renders as markdown (raw HTML escaped by
                 renderMarkdown); user input stays plain text with preserved breaks -->
            <div
              v-if="msg.role === 'assistant'"
              class="message-content markdown-body"
              v-html="renderMarkdown(msg.content)"
            ></div>
            <p v-else class="message-content">{{ msg.content }}</p>
            <!-- Streaming state (last assistant bubble only): breathing typing
                 dots while no answer text has landed yet, then a small
                 "Generating… · N tok/s" meta line fed by the live per-stream
                 counters; the existing statsLine takes over once the stream
                 ends. Rendering only — the stream wiring below is untouched. -->
            <template v-if="idx === messages.length - 1 && streaming && msg.role === 'assistant'">
              <div v-if="!msg.content" class="typing-dots" aria-hidden="true"><i /><i /><i /></div>
              <div class="stream-meta">{{ streamMetaLine }}</div>
            </template>
            <span v-if="idx === messages.length - 1 && streaming" class="streaming-cursor" />
            <!-- Per-phase token rates footer, present after streaming ends -->
            <div v-if="statsLine(msg)" class="message-stats">{{ statsLine(msg) }}</div>
          </div>
        </div>
      </template>
      </div>
    </div>

    <!-- Input area -->
    <div class="input-area">
      <!-- Auto-start / model-switch notices (frame ⑦ .notify): an ARRAY model —
           starting, switching and error flags are independent, so several cards
           can stack. Desktop keeps the single pill in flow (the flags never
           overlap there in practice); the phone tier floats the stack above
           the composer as anchored cards (media-scoped); tablet tiers render
           the same notices INLINE at the top of the conversation instead (the
           .chat-precheck-stack copy inside .messages-area). -->
      <div v-if="!isTabletTier && activeNotices.length" class="start-notice-stack" role="status">
        <div
          v-for="notice in activeNotices"
          :key="notice.kind"
          class="start-notice"
          :class="{ 'start-notice--error': notice.kind === 'error' }"
        >
          <template v-if="notice.kind === 'error'">
            <span class="start-notice-text">{{ notice.text }}</span>
            <button v-if="notice.cause === 'needModels'" class="start-notice-btn" @click="goDownloads">
              {{ t('action.gotoDownloads') }}
            </button>
            <button v-else-if="notice.cause === 'needRuntime'" class="start-notice-btn" @click="goRuntime">
              {{ t('chat.goRuntime') }}
            </button>
          </template>
          <template v-else>
            <span v-if="notice.kind === 'starting'" class="start-notice-spinner" aria-hidden="true"></span>
            <span>{{ notice.text }}</span>
          </template>
        </div>
      </div>
      <!-- Pending attachment preview bar -->
      <div v-if="pendingImages.length" class="pending-bar">
        <div class="pending-item" v-for="(img, i) in pendingImages" :key="i">
          <img :src="img" class="pending-thumb" alt="" />
          <button class="pending-remove" @click="removePendingImage(i)" :title="t('chat.removeImage')">✕</button>
        </div>
      </div>
      <div class="input-row" :class="{ 'input-row--blocked': composerBlocked }">
        <!-- Vision gate (issue #35): the attach entry is disabled only when a
             model IS selected and it cannot take images (no sibling mmproj and
             no explicit projector override) — with no selection, attachments
             stay open and the send-time fallback guides instead. -->
        <button class="attach-btn" @click="triggerAttach" :disabled="attachBlocked" :title="attachBlocked ? t('chat.attachBlocked') : t('chat.attach')" type="button">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/>
          </svg>
        </button>
        <input
          ref="fileInput"
          type="file"
          accept="image/*"
          multiple
          class="file-input-hidden"
          @change="onFileSelected"
        />
        <textarea
          ref="inputBox"
          class="chat-input"
          rows="1"
          :placeholder="inputPlaceholder"
          :disabled="serviceStarting || streaming || (!selectedModel && !isMobileTier)"
          @keydown="onInputKeydown"
          @input="onInputResize"
          @paste="onInputPaste"
        ></textarea>
        <!-- Design frame ② composer: circular gradient send button that
             flips to a red circular stop button while streaming — the state
             must read at a glance, so the icons + aria-labels swap with it.
             Phone tier (frame ⑦): with no model selected the button stays
             enabled so tapping it runs the chatReadiness precheck, which
             surfaces the guided "no models" notice + download CTA instead of
             a dead button; the desktop keeps the disabled gate unchanged. -->
        <button
          v-if="!streaming"
          class="send-btn"
          :disabled="serviceStarting || (!selectedModel && !isMobileTier)"
          :aria-label="t('chat.send')"
          :title="t('chat.send')"
          @click="send"
          type="button"
        >
          <svg width="19" height="19" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M3 11.5L21 3l-8.5 18-2.3-7.2L3 11.5z"/>
          </svg>
        </button>
        <button
          v-else
          class="send-btn stop-btn"
          :aria-label="t('chat.stop')"
          :title="t('chat.stop')"
          @click="stop"
          type="button"
        >
          <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <rect x="5.5" y="5.5" width="13" height="13" rx="2.5"/>
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick, watch, onUnmounted, type ComponentPublicInstance, type Ref } from 'vue'
import { useRouter } from 'vue-router'
import { getServerStatus, getServerConfig, getModels, getModelConfig, getLlamaCpp, startServerWithModel, unloadModel } from '../wails'
import {
  chatErrorKind,
  chatParamsLayout,
  chatReadiness,
  directModeNeedsSwitch,
  fetchRouterModels,
  isVisionCapable,
  modelsToUnload,
  streamChatCompletion,
  tokenRates,
  visionConfigKey,
  type BuildChatBodyOptions,
  type ChatReadiness,
} from '../lib/chat'
import {
  GEN_PRESET_DEFAULT_ID,
  clampPresetSamplingField,
  loadCustomGenPresets,
  loadSelectedGenPresetId,
  mergedGenPresets,
  persistCustomGenPresets,
  persistSelectedGenPresetId,
  presetSamplingFields,
  removeCustomPreset,
  resolveGenPreset,
  withNewCustomPreset,
  type GenPreset,
  type GenPresetSampling,
} from '../lib/genPresets'
import { messages, selectedModel, streaming, chatAbortController, persistChat, reconcileSelectedModel, chatParams, persistChatParams, stepMaxTokens, isUnlimitedMaxTokens, type ChatMessage, type ChatParams } from '../lib/chatState'
import { nudgeDock } from '../lib/dockNudge'
import { dockLane, dockWidth } from '../lib/dockSpace'
import { t } from '../lib/i18n'
import { renderMarkdown } from '../lib/markdown'
import { handleLinkClick } from '../lib/linkHandler'
import { isNearBottom } from '../lib/scroll'
import { usePlatform } from '../lib/platform'
import ThemedSelect, { type SelectOption } from '../components/ThemedSelect.vue'

const router = useRouter()
const platform = usePlatform()

/** Phone-tier gate (viewport width <= 767, reactive): picks the params sheet
 * over the popover and the phone-only copy/empty-state variants. */
const isMobileTier = computed(() => platform.value.isMobile)

/**
 * Tablet-tier gate (portrait band 768..1099 on any OS): drives the inline
 * precheck banner, the dimmed-history state, the red model-chip dot and the
 * draft's short composer copy.
 */
const isTabletTier = computed(() => platform.value.isTablet)

/**
 * Which surface renders the inference-params editor (tablet draft frame ⑤):
 * desktop popover / phone sheet / tablet-portrait modal. Pure classifier in
 * lib/chat.ts; drives the popover + Teleport gates below.
 */
const paramsLayout = computed(() => chatParamsLayout(isMobileTier.value, isTabletTier.value))

/** The params editor renders as a Teleported overlay (phone sheet or tablet-portrait modal). */
const overlayParams = computed(() => paramsLayout.value === 'sheet' || paramsLayout.value === 'modal')

/**
 * Desktop-only reserve lanes (see the template note): the phone tier never
 * narrows the chat page for the capsule — its composer spans the symmetric
 * 16px gutters at every capsule position, and TaskDock keeps the capsule
 * clear of the composer by clamping its floor instead.
 */
const laneLeftActive = computed(
  () => !isMobileTier.value && dockLane.value === 'left' && dockWidth.value > 0
)
const laneRightActive = computed(
  () => !isMobileTier.value && dockLane.value === 'right' && dockWidth.value > 0
)

const serverRunning = ref(false)

/**
 * Chat-initiated server bring-up in progress: disables the input row, shows
 * the starting notice, and blocks re-entrant send() calls.
 */
const serviceStarting = ref(false)

/** Model-swap unload pass in progress: brief, shows the switching notice. */
const switchingModel = ref(false)

/** Inline auto-start error text ('' = none; cleared at the next start attempt). */
const startError = ref('')

/** Guided CTA cause for startError: which page fixes the reported blocker. */
const startErrorCause = ref<'needModels' | 'needRuntime' | ''>('')

/**
 * Degraded-composer state (phone frame ⑦): a precheck blocker is on screen,
 * so the composer dims and the placeholder points at the fix.
 */
const composerBlocked = computed(() => startErrorCause.value !== '')

/** One floating notice card. Error cards carry the guided-fix cause; 'info'
 * cards are transient model-list notices (#33). */
interface PageNotice {
  kind: 'starting' | 'switching' | 'error' | 'info'
  text: string
  cause: '' | 'needModels' | 'needRuntime'
}

/**
 * Independent notice flags as a stack (frame ⑦): starting, switching and the
 * last start error are not mutually exclusive states, so each contributes its
 * own card instead of the previous v-if / else-if chain picking a single one.
 */
const activeNotices = computed<PageNotice[]>(() => {
  const out: PageNotice[] = []
  if (serviceStarting.value) {
    out.push({
      kind: 'starting',
      text: platform.value.isAndroid ? t('chat.startingServerAndroid') : t('chat.startingServer'),
      cause: '',
    })
  }
  if (switchingModel.value) out.push({ kind: 'switching', text: t('chat.switchingModel'), cause: '' })
  if (startError.value) out.push({ kind: 'error', text: startError.value, cause: startErrorCause.value })
  if (modelNotice.value) out.push({ kind: 'info', text: modelNotice.value, cause: '' })
  return out
})

/** Local (scanned) models backing the picker; independent of server state.
 *  hasMmproj (GetModels already returns it) feeds the chat vision gate. */
const localModels = ref<{ name: string; alias?: string; hasMmproj?: boolean }[]>([])

// ─── Vision capability of the selected model (issue #35) ────────────────────
// Visual models need the main weights GGUF plus an mmproj projector GGUF. The
// capability verdict mirrors the backend preset rules: a sibling mmproj file
// (hasMmproj from the scan) OR an explicit mmproj path saved in the model
// settings (core/preset.go writes the explicit line even when the same-dir
// scan is false). The verdict drives the attach-entry gate, the pending-image
// drop and the send-time fallback; raw server errors are re-mapped on arrival.

/** Explicit mmproj path from the selected model's saved settings ('' = none). */
const mmprojOverride = ref('')

// Load the override whenever the selection OR the scanned list changes: on
// mount the persisted selection exists before localModels fills, so watching
// both re-resolves once refreshLocalModels lands (the re-fetch is acceptable —
// the backend answers from an in-memory config map). The selected router id is
// resolved to the display Name first via lib/chat.ts visionConfigKey, because
// per-model configs are keyed by Name (core/preset.go / ModelSettings.vue),
// not by the router alias. The synchronous reset keeps the PREVIOUS model's
// override from leaking into the new selection while the fetch is in flight,
// and the sequence guard drops stale responses from a fast switch. Load
// failure degrades to '' (auto-detect verdict only), never blocks the page.
let mmprojLoadSeq = 0
watch([selectedModel, localModels], async ([id]) => {
  mmprojOverride.value = ''
  if (!id) return
  const seq = ++mmprojLoadSeq
  try {
    const cfg = (await getModelConfig(visionConfigKey(localModels.value, id))) as { mmproj?: unknown } | null
    if (seq !== mmprojLoadSeq) return
    mmprojOverride.value = typeof cfg?.mmproj === 'string' ? cfg.mmproj : ''
  } catch {
    mmprojOverride.value = ''
  }
})

/**
 * Whether the currently selected model can take image input (lib/chat.ts
 * isVisionCapable). The picker's option value is the router id — ModelInfo
 * alias falling back to the display name — so lookup mirrors that identity.
 */
const selectedModelHasVision = computed(() => {
  const sel = localModels.value.find((m) => (m.alias || m.name) === selectedModel.value)
  return isVisionCapable(sel?.hasMmproj ?? false, mmprojOverride.value)
})

/**
 * Attachment-entry gate: blocked only when a model IS picked and it cannot see
 * images. With no selection attachments stay ungated (no nagging before a
 * model is even chosen) — the send-time fallback covers that case instead.
 */
const attachBlocked = computed(() => !!selectedModel.value && !selectedModelHasVision.value)

/** Whether the chat parameters panel is expanded */
const showParams = ref(false)

/**
 * Model options for the picker: value is the llama-server model id —
 * ModelInfo.alias is the display Name sanitized for INI/router use and
 * differs from Name when the name has spaces/special chars or collides
 * (core/gguf.go aliasDedup) — falling back to the name when absent. Label
 * stays the human-readable display name.
 */
const modelOptions = computed<SelectOption[]>(() => localModels.value.map((m) => ({ value: m.alias || m.name, label: m.name })))

/**
 * Composer placeholder: the desktop copy documents the Enter / Shift+Enter
 * keyboard affordances, which are meaningless on touch keyboards and wrap to
 * ~3 lines inside the narrow phone input. Touch tiers (phone + tablet, both
 * reactive across breakpoints) get the short copy instead; desktop keeps the
 * original text and behavior unchanged. A blocked precheck (no models /
 * runtime missing) swaps in the guided copy on the touch tiers — and an empty
 * model directory blocks the composer outright, so the same guided copy shows
 * there (tablet draft frames A⑦ "先下载模型后即可发送").
 */
const inputPlaceholder = computed(() => {
  if ((isMobileTier.value || isTabletTier.value) && (composerBlocked.value || modelOptions.value.length === 0)) {
    return t('chat.blockedPlaceholder')
  }
  return isMobileTier.value || isTabletTier.value ? t('chat.inputPlaceholderShort') : t('chat.inputPlaceholder')
})

/**
 * Model-chip placeholder (frame ⑦): with an empty directory the touch-tier
 * chip reads "暂无可用模型" instead of the bare "模型" label; desktop keeps the
 * original placeholder text.
 */
const chipPlaceholder = computed(() =>
  (isMobileTier.value || isTabletTier.value) && modelOptions.value.length === 0 ? t('chat.noModels') : t('chat.model')
)

/**
 * Phone params-sheet control ranges: max tokens is the only remaining stepper
 * (the four sampling fields are owned by the generation preset, see below).
 */
/** Max-tokens stepper with the unlimited (-1) sentinel mapping. */
function applyMaxTokensStep(dir: -1 | 1): number {
  return stepMaxTokens(chatParams.maxTokens, dir)
}

// ─── Generation presets (lib/genPresets.ts) ──────────────────────────────────
// Sampling quick profiles next to the model capsule. "默认" sends NO sampling
// fields (llama-server's saved per-model params apply); any other preset
// attaches its temperature/top_p/top_k/repeat_penalty to the request body
// (verified against llama.cpp b10689: server-schema.cpp evaluates the four
// field_num entries when present, and oaicompat_chat_params_parse copies
// them through). The params editor shows the active preset read-only; only
// max_tokens and the system prompt stay editable per conversation.

const customGenPresets = ref<GenPreset[]>(loadCustomGenPresets())
const genPresetId = ref(loadSelectedGenPresetId())
const genPresetDialogOpen = ref(false)
const genPresetSaveName = ref('')
const genPresetError = ref('')
/**
 * Editable sampling draft of the save-as editor: initialized from the active
 * preset when the dialog opens; the saved custom preset carries the edited
 * values (builtins themselves stay read-only). null = default preset (editor
 * hidden, no sampling to save).
 */
const draftSampling = ref<GenPresetSampling | null>(null)

const genPresetList = computed(() => mergedGenPresets(customGenPresets.value))

/** Active preset with delete-fallback to the default entry. */
const currentGenPreset = computed(() => resolveGenPreset(genPresetList.value, genPresetId.value))

const genPresetOptions = computed<SelectOption[]>(() =>
  genPresetList.value.map((p) => ({ value: p.id, label: p.builtin ? t(p.nameKey!) : p.name || p.id }))
)

const currentGenPresetLabel = computed(() => {
  const p = currentGenPreset.value
  return p.builtin ? t(p.nameKey!) : p.name || p.id
})

/** Compact "temp 0.7 / top_p 0.9 / top_k 40 / rep 1.1" readout; '' for default. */
const presetSummaryText = computed(() => {
  const f = presetSamplingFields(currentGenPreset.value)
  if (!f) return ''
  return `temp ${f.temperature} / top_p ${f.top_p} / top_k ${f.top_k} / rep ${f.repeat_penalty}`
})

/** Save-as needs a sampling-carrying source (the default preset has none). */
const canSaveGenPreset = computed(() => !!currentGenPreset.value.sampling)

function pickGenPreset(id: string) {
  genPresetId.value = id
  persistSelectedGenPresetId(id)
}

function openPresetDialog() {
  genPresetSaveName.value = ''
  genPresetError.value = ''
  // The draft starts from the active preset's values (a copy — editing never
  // touches the builtin definition).
  const src = currentGenPreset.value.sampling
  draftSampling.value = src ? { ...src } : null
  genPresetDialogOpen.value = true
}

function closePresetDialog() {
  genPresetDialogOpen.value = false
  genPresetSaveName.value = ''
  genPresetError.value = ''
  draftSampling.value = null
}

/** Draft field edit: parse and clamp onto the genPresets bounds grid. */
function onDraftField(field: keyof GenPresetSampling, e: Event): void {
  if (!draftSampling.value) return
  const raw = parseFloat((e.target as HTMLInputElement).value)
  const clamped = clampPresetSamplingField(field, raw)
  draftSampling.value = { ...draftSampling.value, [field]: clamped }
  // Reflect the clamped value back into the input (Number inputs keep typed
  // garbage like "1.2345" otherwise).
  ;(e.target as HTMLInputElement).value = String(clamped)
}

/** Save-as: capture the edited draft sampling values under a new name. */
function saveAsGenPreset() {
  const src = draftSampling.value
  if (!src) return
  const r = withNewCustomPreset(customGenPresets.value, genPresetSaveName.value, src)
  if (!r.ok) {
    genPresetError.value =
      r.reason === 'cap' ? t('chat.presetCapReached')
      : r.reason === 'sampling' ? t('chat.presetBadSampling')
      : t('chat.presetBadName')
    return
  }
  customGenPresets.value = r.list
  persistCustomGenPresets(customGenPresets.value)
  genPresetId.value = r.preset.id
  persistSelectedGenPresetId(r.preset.id)
  closePresetDialog()
}

function deleteGenPreset(id: string) {
  customGenPresets.value = removeCustomPreset(customGenPresets.value, id)
  persistCustomGenPresets(customGenPresets.value)
  if (genPresetId.value === id) {
    genPresetId.value = GEN_PRESET_DEFAULT_ID
    persistSelectedGenPresetId(GEN_PRESET_DEFAULT_ID)
  }
}

/**
 * Small header shown on assistant bubbles (design frame ②): the answering
 * model's human-readable display label, falling back to the generic
 * "Assistant" when the selection is missing from the scanned list.
 */
const assistantLabel = computed<string>(() => {
  const opt = modelOptions.value.find((o) => o.value === selectedModel.value)
  return opt?.label || t('chat.assistant')
})

// ─── Live streaming rate (display only) ──────────────────────────────────────
// Per-stream token counters already lived in send(); these refs mirror them
// into the "Generating… · N tok/s" meta line (design frame ②). Purely visual:
// the SSE parsing / persistence contracts in lib/chat.ts are untouched.

/** Live answer-phase tok/s while the stream runs; null outside streaming. */
const liveAnswerTps = ref<number | null>(null)

/** Live reasoning-phase tok/s shown until the first answer delta lands. */
const liveReasoningTps = ref<number | null>(null)

/** Streaming meta copy: "Generating…" plus the active phase's live tok/s. */
const streamMetaLine = computed<string>(() => {
  const tps = liveAnswerTps.value ?? liveReasoningTps.value
  return tps !== null ? `${t('chat.generating')} · ${tps.toFixed(1)} tok/s` : t('chat.generating')
})

// ─── Model-chip state (tablet draft frame A⑦/B⑦) ─────────────────────────────

/**
 * Model-chip dot turns red while a precheck blocker is on screen (tablet
 * draft frames A⑦/B⑦ .mchip red dot); desktop/phone keep the brand gradient.
 */
const chipDotDown = computed(() => isTabletTier.value && composerBlocked.value)

const messagesContainer = ref<HTMLDivElement | null>(null)
const inputBox = ref<HTMLTextAreaElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

/** Pending images to send (data URLs), cleared after sending */
const pendingImages = ref<string[]>([])

/** Explicit user toggles of reasoning-block expansion, keyed by message index (component-local; never persisted) */
const reasoningExpanded = ref<Record<number, boolean>>({})

/**
 * Effective reasoning-block expansion: an explicit user toggle wins; otherwise the
 * block auto-expands only while streaming the last assistant message that has
 * reasoning but no answer content yet, and auto-collapses once content starts
 * arriving or streaming ends.
 */
function isReasoningExpanded(idx: number, msg: ChatMessage): boolean {
  if (idx in reasoningExpanded.value) return reasoningExpanded.value[idx]
  return idx === messages.value.length - 1 && streaming.value && !!msg.reasoning && !msg.content
}

/** Toggle the reasoning block, recording an explicit override for this message. */
function toggleReasoning(idx: number) {
  const msg = messages.value[idx]
  if (!msg) return
  reasoningExpanded.value[idx] = !isReasoningExpanded(idx, msg)
}

/**
 * Reasoning-block header copy (frame ⑥ .think): the touch tiers state the
 * block's live phase — "deep thinking" while reasoning deltas stream in with
 * no answer text yet, "deep thought" once done; desktop keeps the original
 * static label.
 */
function thinkingLabel(idx: number, msg: ChatMessage): string {
  if (!isMobileTier.value && !isTabletTier.value) return t('chat.thinking')
  const active = idx === messages.value.length - 1 && streaming.value && !!msg.reasoning && !msg.content
  return active ? t('chat.thinkingActive') : t('chat.thinkingDone')
}

// ─── Reasoning body stick-to-bottom ─────────────────────────────────────────

/** Reasoning-body element of the last message (stick-to-bottom scroll target); null when collapsed or unmounted */
const reasoningBodyEl = ref<HTMLDivElement | null>(null)

/** Whether the reasoning body is pinned to its bottom; flipped false when the user scrolls up to read earlier thinking */
const reasoningStuck = ref(true)

/**
 * Function ref for the last message's reasoning body: captures the element and
 * resets the stick state when a NEW element appears (a fresh block starts
 * pinned). Vue re-invokes function refs on every patch with the same element,
 * so the identity guard keeps per-delta re-invocations from resetting a
 * user-scrolled-up state. Element-null transitions (collapse, message stops
 * being last, component unmount/navigation) simply clear the capture.
 */
function setReasoningBodyRef(el: Element | ComponentPublicInstance | null): void {
  const dom = el instanceof HTMLDivElement ? el : null
  if (dom === reasoningBodyEl.value) return
  reasoningBodyEl.value = dom
  reasoningStuck.value = true
}

/** Record near-bottom state from user scrolling; ignores bodies other than the captured target. */
function onReasoningScroll(e: Event) {
  const el = reasoningBodyEl.value
  if (!el || e.target !== el) return
  reasoningStuck.value = isNearBottom(el.scrollTop, el.scrollHeight, el.clientHeight)
}

/**
 * Keep the expanded reasoning body pinned to its bottom while reasoning deltas
 * stream in — but only while the user is themselves near the bottom, so a user
 * reading earlier thinking is never yanked around. Runs after nextTick so the
 * DOM (and scrollHeight) reflects the appended delta; the null check follows
 * scrollToBottom's style and guards element absence after unmount/navigation.
 */
function scrollReasoningToBottom() {
  nextTick(() => {
    const el = reasoningBodyEl.value
    if (el && reasoningStuck.value) el.scrollTop = el.scrollHeight
  })
}

/** One-line stats footer, e.g. "思考 45.2 tok/s · 生成 38.6 tok/s"; each part shown only when defined. */
function statsLine(msg: ChatMessage): string {
  if (!msg.stats) return ''
  const parts: string[] = []
  if (msg.stats.reasoningTps !== undefined) parts.push(t('chat.statsThinking', { v: msg.stats.reasoningTps.toFixed(1) }))
  if (msg.stats.answerTps !== undefined) parts.push(t('chat.statsAnswer', { v: msg.stats.answerTps.toFixed(1) }))
  return parts.join(' · ')
}

/** Mirror a phase's running tok/s into its live ref (tokens / elapsed seconds); guarded against a zero elapsed window. */
function updateLiveTps(target: Ref<number | null>, tokens: number, startedAt: number): void {
  const elapsed = (performance.now() - startedAt) / 1000
  if (elapsed > 0) target.value = tokens / elapsed
}

/**
 * Fetch the local (scanned) model list and reconcile the persisted selection
 * against the available router ids (alias, falling back to the display name —
 * the same strings the picker and the router expose). Runs on mount
 * (independent of server state) and again after a successful auto-start, so a
 * freshly stocked directory is picked up without leaving the page.
 * Self-heals a stale persisted pick (#33): a vanished model switches to the
 * first available one with a light notice; an empty directory clears the
 * selection and points at the downloads.
 */
async function refreshLocalModels(): Promise<void> {
  try {
    const list = await getModels()
    localModels.value = Array.isArray(list) ? list : []
  } catch {
    // Backend unavailable (standalone vite) or scan failure: keep current state
    return
  }
  const ids = localModels.value.map((m) => m.alias || m.name)
  const reconciled = reconcileSelectedModel(selectedModel.value, ids)
  if (reconciled.action === 'switched') {
    selectedModel.value = reconciled.model
    persistChat()
    const label = modelOptions.value.find((o) => o.value === reconciled.model)?.label || reconciled.model
    showModelNotice(t('chat.modelSwitched', { name: label }))
  } else if (reconciled.action === 'cleared') {
    selectedModel.value = ''
    persistChat()
    showModelNotice(t('chat.pickAfterDownload'))
  }
}

/**
 * Transient model-list notice (#33): a one-line info card in the notices
 * stack that auto-dismisses after a few seconds (light toast semantics).
 */
const modelNotice = ref('')
let modelNoticeTimer: ReturnType<typeof setTimeout> | null = null

function showModelNotice(text: string): void {
  modelNotice.value = text
  if (modelNoticeTimer) clearTimeout(modelNoticeTimer)
  modelNoticeTimer = setTimeout(() => {
    modelNotice.value = ''
  }, 6000)
}

/**
 * Chat-initiated server bring-up: pre-check models/runtime so blockers surface
 * as guided errors (no pointless start call), then start llama-server for the
 * selected model and poll the router until it answers /models (up to ~30s on
 * desktop, ~60s on Android — loading from phone flash is slow). Desktop
 * (router mode) ignores the model and lazy-loads; Android (direct mode) makes
 * it the single resident, restarting the service when another model is
 * resident. Returns true when the service ended up ready to stream.
 */
async function ensureServerReady(): Promise<boolean> {
  startError.value = ''
  startErrorCause.value = ''
  serviceStarting.value = true
  try {
    let readiness: ChatReadiness = 'ok'
    try {
      const [models, rt] = await Promise.all([getModels(), getLlamaCpp()])
      readiness = chatReadiness(Array.isArray(models) ? models.length : 0, rt?.installed === true)
    } catch {
      // Probe failure must not block the attempt: the start call reports the real error
      readiness = 'ok'
    }
    if (readiness === 'needModels') {
      startError.value = t('chat.noModels')
      startErrorCause.value = 'needModels'
      return false
    }
    if (readiness === 'needRuntime') {
      startError.value = `${t('runtime.llamacpp')} ${t('runtime.llamacpp.notFound')}`
      startErrorCause.value = 'needRuntime'
      return false
    }
    await startServerWithModel(selectedModel.value)
    // Poll router readiness: llama-server binds /models a moment after spawn
    const cfg = await getServerConfig()
    const deadline = Date.now() + (platform.value.isAndroid ? 60000 : 30000)
    while (Date.now() < deadline) {
      try {
        await fetchRouterModels(cfg.port, { apiKey: cfg.apiKey })
        // Router answered: the service is ready to stream
        serverRunning.value = true
        await refreshLocalModels()
        nudgeDock()
        return true
      } catch {
        // Not ready yet; retry after the poll interval
        await new Promise((r) => setTimeout(r, 500))
      }
    }
    startError.value = t('api.toggleFailed', { msg: 'timeout' })
    return false
  } catch (e: any) {
    // Start rejections already carry user-facing localized backend errors
    startError.value = e?.message || String(e)
    return false
  } finally {
    serviceStarting.value = false
  }
}

/**
 * Deterministic single-model memory: before streaming, unload every OTHER
 * loaded model so the selected one becomes the only resident. Best-effort:
 * individual unload failures are ignored and never block the chat request.
 * Skipped entirely on Android: direct mode always has exactly one resident
 * (the backend start binding restarts the service when the selected model
 * differs), so there is nothing to unload here.
 */
async function unloadOtherModels(): Promise<void> {
  if (platform.value.isAndroid) return
  let toUnload: string[] = []
  try {
    const cfg = await getServerConfig()
    const loaded = await fetchRouterModels(cfg.port, { apiKey: cfg.apiKey })
    toUnload = modelsToUnload(loaded, selectedModel.value)
  } catch {
    // Router unreachable: let the chat request itself surface the real error
    return
  }
  if (toUnload.length === 0) return
  switchingModel.value = true
  try {
    for (const id of toUnload) {
      try {
        await unloadModel(id)
      } catch {
        // Best-effort: an unload failure must not block streaming
      }
    }
    nudgeDock()
  } finally {
    switchingModel.value = false
  }
}

/**
 * Direct-mode (Android) resident reconciliation: a running direct server
 * keeps exactly one model in memory, and the frontend never restarts it in
 * the background — so if the user picked a different model since the last
 * send, the OLD resident would answer. Probe the resident list (direct-mode
 * fallback built into fetchRouterModels) and restart the service with the
 * selected model via ensureServerReady when it is not resident. Best-effort:
 * a probe failure (service momentarily unreachable) passes through and lets
 * the chat request itself surface the real error; a failed RESTART blocks
 * the send (ensureServerReady already reported the guided error). Returns
 * true when the selected model is resident and streaming may proceed.
 */
async function ensureDirectModeResident(): Promise<boolean> {
  let needsSwitch: boolean
  try {
    const cfg = await getServerConfig()
    const loaded = await fetchRouterModels(cfg.port, { apiKey: cfg.apiKey })
    needsSwitch = directModeNeedsSwitch(
      loaded.filter((m) => m.status === 'loaded').map((m) => m.id),
      selectedModel.value
    )
  } catch {
    // Resident probe failed: pass through, the chat request reports the truth
    return true
  }
  if (!needsSwitch) return true
  // Switching notice + restart: startServerWithModel (inside
  // ensureServerReady) makes the selected model the single resident
  switchingModel.value = true
  try {
    return await ensureServerReady()
  } finally {
    switchingModel.value = false
  }
}

/** Guided fixes for a failed auto-start: model downloads / runtime tab. */
function goDownloads() {
  router.push('/models/download')
}

// Open the runtime tab directly: the smart default on / would land on the
// system info tab when llama.cpp is installed but cudart is missing
function goRuntime() {
  router.push('/runtime')
}

/** Close the params panel when clicking outside it */
function onDocClick() {
  showParams.value = false
}

function pickModel(id: string) {
  selectedModel.value = id
  persistChat()
}

/** Clear conversation messages (preserves selected model preference) and persist. */
function clearChat() {
  messages.value = []
  reasoningExpanded.value = {}
  persistChat()
  inputBox.value?.focus()
  resetInputHeight()
}

/** Reset chat params to defaults (in-flight requests unaffected; takes effect on next send). */
function resetParams() {
  // The four sampling fields are owned by the generation preset (toolbar
  // picker) and are not edited here; reset only covers the editor-owned fields.
  Object.assign(chatParams, {
    maxTokens: -1,
    systemPrompt: '',
  })
  persistChatParams()
}

/** Input auto-resizes to 1-6 rows: triggered by @input, reset after send/clear. */
function onInputResize() {
  const el = inputBox.value
  if (!el) return
  el.style.height = 'auto'
  const maxPx = 1.5 * parseFloat(getComputedStyle(document.documentElement).fontSize) * 6 + 20
  el.style.height = Math.min(el.scrollHeight, maxPx) + 'px'
}

function resetInputHeight() {
  const el = inputBox.value
  if (el) el.style.height = 'auto'
}

function scrollToBottom() {
  nextTick(() => {
    const el = messagesContainer.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

function appendAssistant(content: string) {
  messages.value.push({ role: 'assistant', content })
}

/** Read file as data URL; only image types; silently ignore failures or non-images */
async function readFileAsDataUrl(file: File): Promise<string | null> {
  if (!file.type.startsWith('image/')) {
    return null
  }
  return new Promise((resolve) => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : null)
    reader.onerror = () => resolve(null)
    reader.readAsDataURL(file)
  })
}

function triggerAttach() {
  fileInput.value?.click()
}

/**
 * Single pending-image write path (issue #35): both the file picker and the
 * paste handler funnel here, so the vision gate lives in exactly one place.
 * When a model is selected and it cannot take images, the data URL is silently
 * dropped and a light notice explains the fix; with no selection nothing is
 * dropped (send() guides instead).
 */
function addPendingImage(dataUrl: string): void {
  if (attachBlocked.value) {
    showModelNotice(t('chat.imagesNeedVision'))
    return
  }
  pendingImages.value.push(dataUrl)
}

async function onFileSelected(e: Event) {
  const input = e.target as HTMLInputElement
  const files = input.files
  if (!files) return
  for (const file of Array.from(files)) {
    const dataUrl = await readFileAsDataUrl(file)
    if (dataUrl) {
      addPendingImage(dataUrl)
    }
  }
  // Reset input so the same file can be selected again
  input.value = ''
}

/** Paste handler: extract image/* files from clipboardData */
async function onInputPaste(e: ClipboardEvent) {
  const items = e.clipboardData?.items
  if (!items) return
  const imageFiles: File[] = []
  for (const item of Array.from(items)) {
    if (item.type.startsWith('image/')) {
      const file = item.getAsFile()
      if (file) imageFiles.push(file)
    }
  }
  if (imageFiles.length === 0) return
  for (const file of imageFiles) {
    const dataUrl = await readFileAsDataUrl(file)
    if (dataUrl) {
      addPendingImage(dataUrl)
    }
  }
}

function removePendingImage(index: number) {
  pendingImages.value.splice(index, 1)
}

async function send() {
  // Re-entrancy guards: never interleave streams or start two bring-ups
  if (streaming.value || serviceStarting.value) return
  const input = inputBox.value
  if (!input) return
  const text = input.value.trim()
  if (!text && pendingImages.value.length === 0) return

  // Vision fallback (issue #35): attached images need a vision-capable model —
  // sent anyway, llama-server would reject the whole request with a raw English
  // error. Block here with actionable bilingual guidance instead; images
  // already in the preview bar are kept so the user can retry after fixing the
  // model (download the mmproj or set the projector path in ModelSettings).
  if (pendingImages.value.length > 0 && !selectedModelHasVision.value) {
    showModelNotice(t('chat.imagesNeedVision'))
    return
  }

  // Service offline: auto-start llama-server and wait for readiness before
  // streaming. Guided failures return here with the input left untouched.
  if (!serverRunning.value) {
    const ready = await ensureServerReady()
    if (!ready) return
  }

  // Without a model id there is nothing to address the request to
  if (!selectedModel.value) return

  // Deterministic single-resident memory per platform: desktop router mode
  // unloads every OTHER loaded model; Android direct mode restarts the
  // service when the selected model is not the resident one (a running
  // direct server would otherwise keep answering with the OLD model).
  if (platform.value.isAndroid) {
    const ready = await ensureDirectModeResident()
    if (!ready) return
  } else {
    await unloadOtherModels()
  }

  const imgs = pendingImages.value.length ? [...pendingImages.value] : undefined
  messages.value.push({ role: 'user', content: text, images: imgs })
  persistChat()
  input.value = ''
  pendingImages.value = []
  resetInputHeight()
  appendAssistant('')
  streaming.value = true
  scrollToBottom()

  chatAbortController.current = new AbortController()
  // Generation preset decides the sampling override: a non-default preset sends
  // its own temperature/top_p/top_k/repeat_penalty; the default preset omits the
  // four fields entirely (serverSampling) so llama-server applies the per-model
  // parameters saved in ModelSettings. max_tokens / systemPrompt stay editor-owned.
  const preset = currentGenPreset.value
  const presetSampling = preset.sampling
  const bodyOptions: BuildChatBodyOptions = { serverSampling: !presetSampling }
  const sendParams: ChatParams = presetSampling
    ? {
        ...chatParams,
        temperature: presetSampling.temperature,
        topP: presetSampling.topP,
        topK: presetSampling.topK,
        repeatPenalty: presetSampling.repeatPenalty,
      }
    : { ...chatParams }
  // Per-stream token counters and phase timestamps for tok/s stats; llama-server
  // streams one token per data chunk, so delta counts are exact token counts
  let reasoningTokens = 0
  let answerTokens = 0
  let firstReasoningAt: number | null = null
  let firstAnswerAt: number | null = null
  let requestFailed = false
  liveAnswerTps.value = null
  liveReasoningTps.value = null
  // One config fetch serves both the endpoint port and the API key: with a
  // key configured the stream request must carry the bearer header (#29).
  const sendCfg = await getServerConfig()
  try {
    await streamChatCompletion(
      sendCfg.port,
      selectedModel.value,
      messages.value.filter(m => m.role !== 'assistant' || m.content).map(m => ({ role: m.role as 'user' | 'assistant', content: m.content, images: m.images })),
      (delta) => {
        if (firstAnswerAt === null) firstAnswerAt = performance.now()
        answerTokens++
        updateLiveTps(liveAnswerTps, answerTokens, firstAnswerAt)
        const last = messages.value[messages.value.length - 1]
        if (last && last.role === 'assistant') {
          last.content += delta
          scrollToBottom()
        }
      },
      (reasoning) => {
        if (firstReasoningAt === null) firstReasoningAt = performance.now()
        reasoningTokens++
        updateLiveTps(liveReasoningTps, reasoningTokens, firstReasoningAt)
        const last = messages.value[messages.value.length - 1]
        if (last && last.role === 'assistant') {
          last.reasoning = (last.reasoning || '') + reasoning
          scrollToBottom()
          scrollReasoningToBottom()
        }
      },
      chatAbortController.current.signal,
      sendParams,
      bodyOptions,
      { apiKey: sendCfg.apiKey }
    )
  } catch (e: any) {
    // Stop generation (AbortError): keep generated content; if nothing was generated, remove the empty bubble
    if (e?.name === 'AbortError') {
      const last = messages.value[messages.value.length - 1]
      if (last && last.role === 'assistant' && !last.content) {
        messages.value.pop()
      }
      return
    }
    requestFailed = true
    const last = messages.value[messages.value.length - 1]
    if (last && last.role === 'assistant') {
      const raw = e?.message || String(e)
      // Map llama-server's known image-input rejection (issue #35) to guided
      // bilingual copy; the original message stays as secondary detail inside
      // the guidance text. Everything else keeps the plain error template.
      last.content =
        chatErrorKind(raw) === 'vision-unsupported'
          ? t('chat.errorVision', { msg: raw })
          : t('chat.error', { msg: raw })
    }
  } finally {
    streaming.value = false
    chatAbortController.current = null
    // The live meta line hands over to the definitive statsLine footer
    liveAnswerTps.value = null
    liveReasoningTps.value = null
    // Per-phase tok/s: reasoning phase spans first reasoning delta → first content
    // delta (or stream end when there is no answer); answer phase first content
    // delta → stream end. Skipped on error (the bubble then shows an error message).
    const last = messages.value[messages.value.length - 1]
    if (!requestFailed && last && last.role === 'assistant') {
      const endAt = performance.now()
      const rates = tokenRates(
        reasoningTokens,
        firstReasoningAt !== null ? (firstAnswerAt ?? endAt) - firstReasoningAt : 0,
        answerTokens,
        firstAnswerAt !== null ? endAt - firstAnswerAt : 0
      )
      if (rates.reasoningTps !== undefined || rates.answerTps !== undefined) {
        last.stats = rates
      }
    }
    persistChat()
    scrollToBottom()
    // The streamed request just loaded the selected model: refresh the dock
    // now so it appears without waiting for the 1s poll
    nudgeDock()
  }
}

/**
 * Stop generation: triggered manually by the caller; does NOT abort on component unmount—
 * the stream continues writing to module-level state after navigation, so returning
 * to the page shows the complete reply (scrollToBottom's null check on the ref
 * prevents errors after unmount).
 *
 * chatAbortController lifted to module scope ensures the new instance's stop() after
 * navigation remount can still interrupt the in-flight streaming request.
 */
function stop() {
  chatAbortController.current?.abort()
}

function onInputKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

watch(selectedModel, () => {
  inputBox.value?.focus()
})

onMounted(async () => {
  try {
    const status = await getServerStatus()
    serverRunning.value = status.running
  } catch {
    // Backend unavailable (standalone vite): keep the offline default
  }
  // Picker options come from the local scan, so they work with the service
  // stopped; reconcile the persisted choice against the names that exist
  await refreshLocalModels()
  document.addEventListener('click', onDocClick)
})

onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
  if (modelNoticeTimer) clearTimeout(modelNoticeTimer)
})
</script>

<style scoped>
.chat-page {
  /* No bottom reserve for the task dock: the pill sits in the input row's
     right gap (see the lane paddings below) and shares the send button's
     vertical band, so the page layout simply fills the viewport below the
     titlebar. */
  /* Height chain mirrors the sibling fixed-page shell (.page-fixed in
     global.css): the titlebar band via --titlebar-h (App.vue publishes 36px
     on desktop shells, 0px on android/ios where the bar does not render) and
     the mobile nav band via --mobile-nav-height (phone tier only; html
     .keyboard-open collapses it to 0 while the soft keyboard is up). The old
     hardcoded 36px assumed a desktop titlebar that does not exist on the
     phone: idle it left the composer band tucked under the floating nav, and
     with the keyboard open it stopped the page 36px short of the keyboard
     top instead of resting the composer on it. */
  height: calc(100vh - var(--titlebar-h, 36px) - var(--mobile-nav-height, 0px));
  /* dvh twin: progressive override for mobile browsers reporting a dynamic
     viewport (soft keyboard / browser chrome); no-op where dvh is unsupported */
  height: calc(100dvh - var(--titlebar-h, 36px) - var(--mobile-nav-height, 0px));
  display: flex;
  flex-direction: column;
  /* Base gutters, lane retracted (dockLane 'none': capsule parked away from
     the composer band, or dock hidden): symmetric 48px gutters on desktop.
     .chat-page--dock-right (capsule docked in-band right) reserves room for
     the floating TaskDock pill: the input row (send button) and the messages
     scrollbar stop left of the pill, keeping the pill vertically aligned with
     the send button. The lane follows the pill's MEASURED width (--dock-width,
     published by lib/dockSpace): 16 right offset + pill width + 8 gap, floored
     at the original 72px band (sized for a single-segment ~48px desktop pill)
     so the docked layout is unchanged; the old fixed 72px overflowed when both
     the download and model counters showed (~80px pill overlapping the send
     button). .chat-page--dock-left mirrors the lane to the left padding. The
     0.2s padding transition smooths lane attach/retract (same pattern as
     App.vue's --dock-reserve padding transition). Top padding rides the
     --safe-area-top inset (0 on desktop) so an edge-to-edge status bar cannot
     eat the header. */
  padding: var(--safe-area-top, 0px) 48px 0 48px;
  transition: padding 0.2s ease;
  /* Chat page does not scroll with page: layout fills remaining viewport height, messages area scrolls independently */
}

/* Capsule docked in the composer band, hugging the RIGHT edge (default spot):
   the historical right lane. The max(72px, …) floor applies ONLY while a lane
   is actually reserved — the retracted ('none') state uses the plain 48px
   gutters above. */
.chat-page--dock-right {
  padding: var(--safe-area-top, 0px) max(72px, calc(16px + var(--dock-width, 0px) + 8px)) 0 48px;
}

/* Capsule docked in the composer band, hugging the LEFT edge: mirror the
   reserve lane — the measured-width lane (same max(72px, …) floor as the right
   lane) moves to the LEFT padding so the attach button / message scrollbar
   stay clear of the pill, and the right gutter returns to its original 48px.
   Bound via .chat-page--dock-left (a CSS string variable cannot branch a
   calc). */
.chat-page--dock-left {
  padding: var(--safe-area-top, 0px) 48px 0 max(72px, calc(16px + var(--dock-width, 0px) + 8px));
}

/* ─── Desktop centered column: 860px on >=1100px (design draft D6) ───
   The chat column is centered with auto side margins; the base 48px gutters
   collapse into the centered column. Dock lane paddings are preserved via
   the higher-specificity --dock-right/--dock-left rules below. */
@media (min-width: 1100px) {
  .chat-page {
    max-width: 860px;
    margin-left: auto;
    margin-right: auto;
  }

  /* Dock lanes still apply inside the centered column */
  .chat-page--dock-right {
    padding-right: max(72px, calc(16px + var(--dock-width, 0px) + 8px));
  }

  .chat-page--dock-left {
    padding-left: max(72px, calc(16px + var(--dock-width, 0px) + 8px));
  }
}

/* ─── Tablet centered column: 720px on 768-1099px (design draft D6) ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  .chat-page {
    max-width: 720px;
    margin-left: auto;
    margin-right: auto;
  }
}

/* Never compress the fixed header/input bands; the messages area absorbs overflow instead */
.chat-page .sticky-top {
  flex-shrink: 0;
}

/* ─── Top bar: model capsule chip + round glass actions (design frame ②) ───
   The page title is gone on all tiers; the sidebar / bottom tab bar already
   carry the page identity. */
.chat-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 2px 14px;
  position: relative; /* Anchor for params popover positioning */
}

/* Capsule chip: a scoped :deep() reskin of the ThemedSelect toolbar trigger —
   the button keeps its full ARIA select-only combobox wiring, only the shell
   changes (island surface, 999px radius, 700 weight). The component has no
   icon slot, so the gradient "online" dot is injected via ::before. The chip
   hugs its content and truncates long model names via the value span's
   ellipsis instead of stretching. */
.chat-model-select {
  flex: 0 1 auto;
  min-width: 0;
  max-width: min(420px, 62%);
}

.chat-model-select :deep(.themed-select__trigger) {
  display: inline-flex;
  width: auto;
  min-width: 0;
  max-width: 100%;
  min-height: 40px;
  gap: 8px;
  padding: 9px 14px 9px 16px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 999px;
  box-shadow: var(--shadow-island);
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 700;
}

.chat-model-select :deep(.themed-select__trigger)::before {
  content: '';
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--grad);
  box-shadow: none;
}

.chat-model-select :deep(.themed-select__trigger:hover:not(:disabled)),
.chat-model-select :deep(.themed-select__trigger[aria-expanded='true']) {
  background: var(--bg-secondary);
  border-color: var(--overlay-20);
}

/* Touch press feedback (OS-scoped): mirrors the capsule chip's hover skin.
   Without this, the component-level toolbar :active rule (ThemedSelect.vue)
   wins on the data-os element qualifier and washes the island body out to
   --hover-bg. The button element qualifier keeps this rule above it. */
html[data-os='android'] .chat-model-select :deep(button.themed-select__trigger:active:not(:disabled)),
html[data-os='ios'] .chat-model-select :deep(button.themed-select__trigger:active:not(:disabled)) {
  background: var(--bg-secondary);
  border-color: var(--overlay-20);
}

/* Round glass action buttons (design .chat-top .rnd): 40px circles, island
   surface + soft shadow; the phone tier floors them at the 44px touch target */
.chat-icon-btn {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: 50%;
  box-shadow: var(--shadow-island);
  color: var(--text-secondary);
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
}

.chat-icon-btn:hover:not(:disabled) {
  color: var(--text-primary);
  border-color: var(--overlay-20);
}

.chat-icon-btn:disabled {
  opacity: 0.35;
  cursor: default;
}

.chat-clear-btn {
  margin-left: auto;
}

/* Generation preset picker: same toolbar variant as the model chip, sized
   narrower (preset names are short); the clear button's auto margin keeps the
   right-hand icon group pinned. */
.chat-preset-select {
  flex: 0 1 auto;
  min-width: 0;
  max-width: min(200px, 34%);
}

.chat-preset-select :deep(.themed-select__trigger) {
  min-height: 40px;
}

/* Params panel (popover + sheet/modal): read-only active-preset summary row */
.preset-summary {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
  background: var(--surface);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-sm);
}

.preset-summary-label {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.4px;
}

.preset-summary-value {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-secondary);
  word-break: break-word;
  font-family: var(--font-mono);
}

/* Preset manager dialog (centered card, all tiers) */
.preset-dialog-root {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}

.preset-dim {
  position: absolute;
  inset: 0;
  background: var(--overlay-50, rgba(0, 0, 0, 0.45));
}

.preset-dialog {
  position: relative;
  width: min(420px, 100%);
  max-height: min(560px, 86vh);
  overflow-y: auto;
  background: var(--bg-card);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-lg, 0 18px 50px rgba(0, 0, 0, 0.25));
  padding: 18px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.preset-dialog-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--text-primary);
}

.preset-dialog-current,
.preset-dialog-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.preset-dialog-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
}

.preset-dialog-input {
  width: 100%;
  padding: 9px 12px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-size: 13px;
  font-family: inherit;
  outline: none;
}

.preset-dialog-input:focus {
  border-color: var(--accent);
}

/* Save-as sampling editor: 2×2 grid of small number fields (one column per
   pair on the phone band via the dialog's own max-width), reusing the dialog
   tokens */
.preset-dialog-sampling {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px;
  background: var(--surface);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-sm);
}

.preset-dialog-sampling-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 10px;
}

.preset-dialog-sfield {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.preset-dialog-slabel {
  font-size: 11px;
  font-weight: 600;
  color: var(--text-dim);
}

.preset-dialog-sinput {
  width: 100%;
  padding: 6px 8px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-primary);
  font-size: 12.5px;
  font-family: var(--font-mono);
  outline: none;
}

.preset-dialog-sinput:focus {
  border-color: var(--accent);
}

.preset-dialog-err {
  margin: 0;
  font-size: 12px;
  color: #ef4444;
}

.preset-dialog-custom {
  display: flex;
  flex-direction: column;
  gap: 6px;
  border-top: 1px solid var(--border);
  padding-top: 12px;
}

.preset-dialog-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 7px 10px;
  background: var(--surface);
  border: 1px solid var(--border-light);
  border-radius: var(--radius-sm);
}

.preset-dialog-item-name {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-primary);
  word-break: break-all;
}

.preset-dialog-del {
  flex-shrink: 0;
  padding: 4px 12px;
  background: transparent;
  border: 1px solid rgba(239, 68, 68, 0.35);
  border-radius: var(--radius-sm);
  color: #ef4444;
  font-size: 11.5px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
}

.preset-dialog-del:hover {
  background: rgba(239, 68, 68, 0.08);
}

.preset-dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding-top: 4px;
}

.preset-dialog-btn {
  padding: 8px 18px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  color: var(--text-muted);
  font-size: 12.5px;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
}

.preset-dialog-btn:hover:not(:disabled) {
  background: var(--hover-bg);
}

.preset-dialog-btn--primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}

.preset-dialog-btn--primary:hover:not(:disabled) {
  opacity: 0.88;
  background: var(--accent);
}

.preset-dialog-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.chat-settings-btn[aria-expanded='true'] {
  color: var(--accent-light);
  border-color: rgba(99, 102, 241, 0.45);
}

/* ─── Params Popover (design frame ② skin: floating island + large radius;
       layout / wiring unchanged) ─── */
.params-popover {
  position: absolute;
  right: 0;
  top: calc(100% + 8px);
  z-index: 30;
  width: 320px;
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-island);
  padding: 18px;
}

.params-header {
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.3px;
  color: var(--text-secondary);
  margin-bottom: 12px;
}

.params-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}

.params-row-full {
  flex-direction: column;
  align-items: stretch;
}

.params-label {
  font-size: 13px;
  color: var(--text-secondary);
  white-space: nowrap;
}

.params-input {
  width: 110px;
  padding: 7px 10px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 10px;
  color: var(--text-primary);
  font-size: 13px;
  outline: none;
}

.params-input:focus {
  border-color: rgba(99, 102, 241, 0.4);
}

.params-textarea {
  width: 100%;
  padding: 8px 10px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  color: var(--text-primary);
  font-size: 13px;
  font-family: var(--font-sans);
  line-height: 1.5;
  outline: none;
  resize: vertical;
}

.params-textarea:focus {
  border-color: rgba(99, 102, 241, 0.4);
}

.params-textarea::placeholder {
  color: var(--text-dim);
}

.params-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 10px;
}

.params-reset-btn {
  padding: 7px 14px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--text-secondary);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s;
}

.params-reset-btn:hover {
  background: var(--hover-bg);
  color: var(--text-primary);
  border-color: var(--overlay-20);
}

/* ─── Auto-start / model-switch notice (glass pill above the composer) ─── */
.start-notice {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 500;
  color: var(--text-secondary);
  background: var(--glass);
  border: 1px solid var(--glass-line);
}

/* Error variant: message + guided CTA sharing the .stop-btn color family
   (rgba overlays stay readable on both light and dark themes) */
.start-notice--error {
  justify-content: space-between;
  color: #f87171;
  background: rgba(239, 68, 68, 0.08);
  border-color: rgba(239, 68, 68, 0.25);
}

.start-notice-text {
  flex: 1;
  min-width: 0;
  word-break: break-word;
}

.start-notice-btn {
  padding: 4px 12px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
  background: rgba(239, 68, 68, 0.12);
  color: #f87171;
  border: 1px solid rgba(239, 68, 68, 0.3);
}

.start-notice-btn:hover {
  background: rgba(239, 68, 68, 0.2);
}

.start-notice-spinner {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  border: 2px solid var(--border);
  border-top-color: var(--accent-light);
  animation: notice-spin 0.8s linear infinite;
  flex-shrink: 0;
}

@keyframes notice-spin {
  to { transform: rotate(360deg); }
}

/* ─── Conversation body wrapper ───
   Pure flex pass-through (column) on every tier. Keeping the wrapper
   styleless elsewhere preserves the desktop/phone layout chain
   (.chat-page → messages → input) exactly. */
.chat-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* ─── Tablet precheck banner (tablet draft frames A⑦/B⑦) ───
   Rendered inline at the top of the conversation, filling the content
   column; only mounts behind the isTabletTier gate, so these base styles
   never apply on desktop/phone. Tier card skins live in the Track A band. */
.chat-precheck-stack {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 12px;
}

/* ─── Messages ─── */
.messages-area {
  flex: 1;
  /* min-height: 0 lets this flex child shrink below its content size so
     overflow-y scrolling kicks in; with the default min-height: auto the
     growing conversation pushes .input-area out of the viewport instead */
  min-height: 0;
  overflow-y: auto;
  padding: 8px 0 16px;
}

.empty-hint {
  text-align: center;
  padding: 48px 0;
  color: var(--text-dim);
  font-size: 14px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

.empty-ico {
  font-size: 32px;
  line-height: 1;
}

.empty-title {
  display: block;
  font-size: 15px;
  font-weight: 700;
  color: var(--text-primary);
}

.empty-sub {
  display: block;
  font-size: 13px;
  color: var(--text-muted);
  line-height: 1.6;
}

/* ─── Phone params sheet (design frame ⑤ .dim / .sheet) ───
   Rendered only on the phone tier (isMobileTier), teleported to <body> so the
   dim + sheet float above every page layer. Desktop never mounts this markup.

   The Teleport wrapper in the template is gated by v-if="isMobileTier" (not
   just the inner .params-sheet-root) because a persistent empty Teleport node
   in the vnode tree breaks Vue's out-in <transition> afterLeave callback on
   the chat page: state.isLeaving sticks and the content area stays blank
   after navigating away (Vue×Teleport edge case, vuejs/core#5836). */
.params-sheet-root {
  position: fixed;
  inset: 0;
  z-index: 60;
}

.params-dim {
  position: absolute;
  inset: 0;
  background: rgba(16, 18, 33, 0.42);
  animation: dim-in 0.2s ease;
}

@keyframes dim-in {
  from { opacity: 0; }
  to { opacity: 1; }
}

.params-sheet {
  position: absolute;
  left: 10px;
  right: 10px;
  bottom: calc(10px + var(--safe-area-bottom, 0px) + var(--keyboard-inset, 0px));
  max-height: calc(100vh - 90px);
  max-height: calc(100dvh - 90px);
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
  border-radius: 26px;
  padding: 18px 20px 16px;
  box-shadow: none;
  animation: sheet-up 0.25s ease;
}

@keyframes sheet-up {
  from { transform: translateY(100%); }
  to { transform: translateY(0); }
}

.params-grab {
  width: 40px;
  height: 4px;
  border-radius: 999px;
  background: var(--border);
  margin: 0 auto 12px;
  flex-shrink: 0;
}

.params-sheet-head {
  display: flex;
  align-items: center;
  margin-bottom: 4px;
  flex-shrink: 0;
}

.params-sheet-title {
  font-size: 17px;
  font-weight: 800;
  color: var(--text-primary);
}

.params-reset-link {
  margin-left: auto;
  padding: 8px 0 8px 12px;
  background: transparent;
  border: none;
  font-size: 12px;
  font-weight: 700;
  color: var(--accent-light);
  cursor: pointer;
}

.params-sheet-body {
  min-height: 0;
  overflow-y: auto;
}

.psheet-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 0;
  border-bottom: 1px solid var(--border);
}

.psheet-row:last-child {
  border-bottom: none;
}

.psheet-row--text {
  flex-direction: column;
  align-items: stretch;
}

.psheet-label {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.psheet-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}

.psheet-sub {
  font-size: 11.5px;
  line-height: 1.5;
  color: var(--text-muted);
}

.psheet-ctl {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

/* Native range input reskinned to the mockup .pslider: 4px gradient track on
   a neutral base (--pfill drives the filled portion), 15px white thumb inside
   a 44px-tall hit area. */
.pslider {
  -webkit-appearance: none;
  appearance: none;
  width: 110px;
  height: 44px;
  background: transparent;
  cursor: pointer;
  margin: 0;
}

.pslider::-webkit-slider-runnable-track {
  height: 4px;
  border-radius: 999px;
  background-color: var(--overlay-10);
  background-image: var(--grad);
  background-size: var(--pfill, 0%) 100%;
  background-repeat: no-repeat;
}

.pslider::-webkit-slider-thumb {
  -webkit-appearance: none;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  background: #fff;
  box-shadow: none;
  margin-top: -5.5px;
}

.pslider::-moz-range-track {
  height: 4px;
  border-radius: 999px;
  background-color: var(--overlay-10);
}

.pslider::-moz-range-progress {
  height: 4px;
  border-radius: 999px;
  background: var(--grad);
}

.pslider::-moz-range-thumb {
  width: 15px;
  height: 15px;
  border: none;
  border-radius: 50%;
  background: #fff;
  box-shadow: none;
}

.psheet-val {
  min-width: 38px;
  text-align: right;
  font-family: var(--font-mono);
  font-size: 12.5px;
  font-weight: 700;
  color: var(--text-primary);
}

/* Stepper (frame ⑤ .pstepper): 32px visual circle inside a 44px touch box
   (background-clip keeps the painted circle at 32px under the padding). */
.pstepper {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.pstep-btn {
  width: 44px;
  height: 44px;
  padding: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-card);
  background-clip: content-box;
  border: none;
  border-radius: 50%;
  color: var(--text-secondary);
  font-size: 16px;
  font-weight: 700;
  line-height: 1;
  cursor: pointer;
}

.pstep-btn:active {
  color: var(--accent-light);
}

.pstep-val {
  min-width: 56px;
  text-align: center;
  font-family: var(--font-mono);
  font-size: 12.5px;
  font-weight: 700;
  color: var(--text-primary);
}

/* System prompt textarea (frame ⑤ .ptext) */
.ptext {
  width: 100%;
  padding: 8px 11px;
  background: var(--bg-card);
  border: none;
  border-radius: var(--r-md);
  color: var(--text-primary);
  font-size: 12px;
  font-family: var(--font-sans);
  line-height: 1.5;
  outline: none;
  resize: vertical;
  min-height: 44px;
}

.ptext::placeholder {
  color: var(--text-dim);
}

/* ─── Message bubbles (design frame ②) ───
   User: brand gradient, white text, small radius tucked at the sender corner
   (22/22/6/22). Assistant: lifted island surface, mirrored radius
   (22/22/22/6), small model-name header on top. Colors ride the theme tokens
   so the dark mapping (lifted #161622 family) comes for free. */
.message-row {
  display: flex;
  margin-bottom: 14px;
}

.message-row.is-user {
  justify-content: flex-end;
}

.message-row.is-assistant {
  justify-content: flex-start;
}

.message-bubble {
  max-width: 78%;
  padding: 12px 16px;
  /* No blanket pre-wrap: markdown output manages its own spacing (code must
     not wrap); plain-text spots scope pre-wrap individually */
  font-size: 13.5px;
  line-height: 1.75;
  font-weight: 400;
  word-break: break-word;
}

.is-user .message-bubble {
  background: var(--grad);
  color: #fff;
  border-radius: var(--r-md) var(--r-md) 6px var(--r-md);
  box-shadow: none;
}

.is-assistant .message-bubble {
  background: var(--bg-secondary);
  border: 1px solid var(--border);
  border-radius: var(--r-md) var(--r-md) var(--r-md) 6px;
  box-shadow: var(--shadow-island);
  color: var(--text-primary);
}

/* Assistant-only header: the answering model's display name (design .who) */
.message-role {
  display: block;
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.4px;
  color: var(--text-muted);
  margin-bottom: 5px;
  user-select: none;
}

.message-images {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}

.message-image {
  max-width: 100%;
  border-radius: 8px;
  max-height: 280px;
  object-fit: contain;
}

.message-content {
  margin: 0;
}

/* User input stays plain text: preserve explicit line breaks */
.is-user .message-content {
  white-space: pre-wrap;
}

/* ─── Markdown rendering (assistant bubbles) ───
   v-html content does not carry the scope attribute, so child selectors need
   :deep(). Sizes are relative to the 13px bubble text; colors ride the theme
   CSS variables so light/dark both work. */
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4),
.markdown-body :deep(h5),
.markdown-body :deep(h6) {
  margin: 12px 0 6px;
  line-height: 1.35;
  color: var(--text-primary);
}

.markdown-body :deep(h1) { font-size: 1.35em; }
.markdown-body :deep(h2) { font-size: 1.2em; }
.markdown-body :deep(h3) { font-size: 1.08em; }
.markdown-body :deep(h4),
.markdown-body :deep(h5),
.markdown-body :deep(h6) { font-size: 1em; }

.markdown-body :deep(p) {
  margin: 6px 0;
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  margin: 6px 0;
  padding-left: 1.4em;
}

.markdown-body :deep(li) {
  margin: 2px 0;
}

.markdown-body :deep(li > ul),
.markdown-body :deep(li > ol) {
  margin: 2px 0;
}

/* Inline code: subtle inset chip */
.markdown-body :deep(code) {
  background: var(--overlay-8);
  border: 1px solid var(--border-light);
  border-radius: 4px;
  padding: 0.5px 5px;
  font-size: 0.92em;
  word-break: break-word;
}

/* Fenced code blocks: monospace via the global code/pre rule; long lines
   scroll horizontally instead of wrapping */
.markdown-body :deep(pre) {
  margin: 8px 0;
  padding: 10px 12px;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow-x: auto;
}

.markdown-body :deep(pre code) {
  background: transparent;
  border: none;
  padding: 0;
  font-size: 0.95em;
  line-height: 1.5;
  white-space: pre;
  word-break: normal;
}

.markdown-body :deep(blockquote) {
  margin: 8px 0;
  padding: 2px 0 2px 12px;
  border-left: 3px solid var(--accent-glow);
  color: var(--text-secondary);
}

.markdown-body :deep(blockquote p) {
  margin: 4px 0;
}

/* Tables: block + auto scroll so wide tables stay inside the bubble */
.markdown-body :deep(table) {
  display: block;
  margin: 8px 0;
  border-collapse: collapse;
  overflow-x: auto;
  max-width: 100%;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
  border: 1px solid var(--border);
  padding: 4px 10px;
  text-align: left;
}

.markdown-body :deep(th) {
  background: var(--surface);
  font-weight: 600;
}

.markdown-body :deep(a) {
  color: var(--accent-light);
}

.markdown-body :deep(a:hover) {
  text-decoration: underline;
}

.markdown-body :deep(img) {
  max-width: 100%;
  height: auto;
}

.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid var(--border);
  margin: 12px 0;
}

/* Avoid doubled spacing where the markdown content meets the bubble padding */
.markdown-body > :deep(:first-child) {
  margin-top: 0;
}

.markdown-body > :deep(:last-child) {
  margin-bottom: 0;
}

/* ─── Reasoning (thinking) block ─── */
.reasoning-block {
  margin-bottom: 8px;
}

.reasoning-header {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: transparent;
  border: none;
  padding: 0;
  color: var(--text-muted);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  user-select: none;
}

.reasoning-header:hover {
  color: var(--text-secondary);
}

.reasoning-chevron {
  transition: transform 0.2s;
}

.reasoning-block.expanded .reasoning-chevron {
  transform: rotate(180deg);
}

.reasoning-body {
  margin-top: 4px;
  /* Slightly smaller and muted so long thinking stays secondary to the answer */
  font-size: 12.5px;
  line-height: 1.5;
  font-weight: 500;
  color: var(--text-secondary);
  white-space: pre-wrap;
  max-height: 220px;
  overflow-y: auto;
}

/* ─── Token rate stats footer ─── */
.message-stats {
  margin-top: 6px;
  font-size: 11px;
  color: var(--text-dim);
  user-select: none;
}

/* ─── Streaming typing indicator (design .typing) ───
   Three breathing dots shown in the streaming bubble before the first answer
   text lands; the meta line under it carries the live per-stream tok/s. */
.typing-dots {
  display: inline-flex;
  gap: 4px;
  padding: 4px 0;
}

.typing-dots i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-muted);
  animation: typing-breathe 1.2s infinite;
}

.typing-dots i:nth-child(2) {
  animation-delay: 0.2s;
}

.typing-dots i:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes typing-breathe {
  0%, 60%, 100% { transform: translateY(0); opacity: 0.5; }
  30% { transform: translateY(-4px); opacity: 1; }
}

/* Live "Generating… · N tok/s" line under the streaming bubble */
.stream-meta {
  margin-top: 7px;
  font-size: 10.5px;
  font-weight: 600;
  color: var(--text-muted);
  user-select: none;
}

/* ─── Streaming cursor ─── */
.streaming-cursor {
  display: inline-block;
  width: 6px;
  height: 14px;
  margin-left: 2px;
  vertical-align: text-bottom;
  background: var(--accent-light);
  border-radius: 1px;
  animation: blink 1s steps(2) infinite;
}

@keyframes blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0; }
}

/* ─── Input: floating glass composer (design .composer) ───
   The bar IS the .input-row inside the flex chain — the page's
   height/100dvh chain (soft-keyboard adjustResize) and the --dock-width
   lane paddings are untouched, so the send button's right edge (and with it
   the TaskDock pill's 8px clearance) stays exactly where it was; the glass
   shell is pure skin. */
.input-area {
  display: flex;
  flex-direction: column;
  gap: 10px;
  /* Bottom padding carries the Android keyboard avoidance (--keyboard-inset
     from lib/safeArea.ts): the composer must clear the IME while the fixed
     tab bar stays pinned underneath it. Desktop / keyboard-down: the var is
     0px and the 24px base rhythm applies unchanged. */
  padding: 12px 0 calc(24px + var(--keyboard-inset, 0px));
  /* Stays fixed at the bottom: never compressed by the flex column */
  flex-shrink: 0;
}

.input-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 6px 6px 10px;
  background: var(--glass);
  border: 1px solid var(--glass-line);
  border-radius: var(--r-lg);
  box-shadow: var(--shadow-island);
  transition: border-color 0.2s;
}

.input-row:focus-within {
  border-color: rgba(99, 102, 241, 0.45);
}

.pending-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.pending-item {
  position: relative;
  width: 64px;
  height: 64px;
  border-radius: 8px;
  overflow: hidden;
  border: 1px solid var(--border);
}

.pending-thumb {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.pending-remove {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 18px;
  height: 18px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  border: none;
  border-radius: 50%;
  font-size: 10px;
  cursor: pointer;
  line-height: 1;
}

.attach-btn {
  width: 42px;
  height: 42px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: none;
  border-radius: 50%;
  color: var(--text-secondary);
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
}

.attach-btn:hover:not(:disabled) {
  background: var(--hover-bg);
  color: var(--text-primary);
}

/* Vision-gated attach entry (issue #35): dimmed with a not-allowed cursor so
   the blocked state reads at a glance (the title carries the reason). */
.attach-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

/* Touch press feedback (OS-scoped): :hover never fires on touch input, so
   the active state mirrors the hover visuals under html[data-os]. */
html[data-os='android'] .attach-btn:active,
html[data-os='ios'] .attach-btn:active {
  background: var(--hover-bg);
  color: var(--text-primary);
}

.file-input-hidden {
  display: none;
}

.chat-input {
  flex: 1;
  resize: none;
  padding: 10px 4px;
  background: transparent;
  border: none;
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 500;
  font-family: var(--font-sans);
  line-height: 1.5;
  outline: none;
  /* Auto-resize 1-6 rows; focus feedback lives on the glass bar
     (.input-row:focus-within), not on the naked textarea */
  min-height: 42px;
  max-height: calc(1.5em * 6 + 20px);
  overflow-y: auto;
}

.chat-input::placeholder {
  color: var(--text-dim);
}

/* Circular gradient send button (design .composer .send): gradient = the one
   actionable element. Desktop keeps the 42px band so the TaskDock pill's
   vertical centering (dockSpace DOCK_BOTTOM_OFFSET 29px) is unchanged. */
.send-btn {
  width: 42px;
  height: 42px;
  padding: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--grad);
  color: #fff;
  border: none;
  border-radius: 50%;
  cursor: pointer;
  transition: all 0.2s;
  flex-shrink: 0;
  box-shadow: none;
}

.send-btn:hover:not(:disabled) {
  filter: brightness(1.08);
  box-shadow: none;
}

/* Touch press feedback (OS-scoped): mirrors the hover lift on touch (also
   covers the streaming stop state, which shares this class). */
html[data-os='android'] .send-btn:active:not(:disabled),
html[data-os='ios'] .send-btn:active:not(:disabled) {
  filter: brightness(1.08);
  box-shadow: none;
}

.send-btn:disabled {
  opacity: 0.35;
  cursor: default;
  box-shadow: none;
}

/* Streaming state flips the same circle to danger red (design frame ⑥:
   red is reserved for stop/unload) with a square stop glyph */
.stop-btn {
  background: var(--danger);
  box-shadow: none;
}

.stop-btn:hover:not(:disabled) {
  filter: brightness(1.08);
  box-shadow: none;
}

/* ─── Mobile (<=767px): shell sizing + compact composer ───
   The custom title bar follows the OS-scoped supportsFramelessTitlebar
   capability (absent on android/ios) and the bottom tab bar takes its own
   band at this breakpoint, so the page must fill the visible viewport between
   the two: --titlebar-h (36px/0px) and --mobile-nav-height (0px on desktop)
   make this rule an exact no-op on desktop. The flex column (header /
   messages / composer) plus the viewport-height chain keeps the composer
   visible when the soft keyboard resizes the window (AndroidManifest
   adjustResize) — the 100dvh twin tracks the same chain where the browser
   reports a dynamic viewport. Right padding keeps the TaskDock pill band
   (16 right offset + 40 phone pill width + 8 gap = 64, see TaskDock.vue's
   phone block); left padding shrinks to the shared 16px phone gutter, and the
   top rides --safe-area-top (0 on desktop) for edge-to-edge status bars. */
.chat-page {
  height: calc(100vh - var(--titlebar-h, 36px) - var(--mobile-nav-height, 0px));
  /* dvh twin: progressive override, no-op where dvh is unsupported */
  height: calc(100dvh - var(--titlebar-h, 36px) - var(--mobile-nav-height, 0px));
}

@media (max-width: 767px) {
  .chat-page {
    /* Symmetric 16px phone gutters at EVERY capsule position: the phone tier
       reserves no dock lane (the desktop lane classes are gated off in the
       template) — narrowing the page for a floating capsule wasted reading
       width on a phone. TaskDock instead clamps the capsule above the
       composer's top edge on this route, so the full-width input row and its
       send button stay tappable. */
    padding: var(--safe-area-top, 0px) 16px 0 16px;
  }

  /* Carve-out from the global phone-tier unstick (global.css .sticky-top
     static): the chat header band is fixed-viewport chrome — .page-fixed's
     flex-shrink:0 band holding the model chip — and .chat-page never scrolls,
     so sticky never engaged here anyway. Re-pin it as position:relative so
     the band occupies the exact same box and still honors the shared
     z-index:20 stacking above the messages band, pixel-identical to before.
     top:auto cancels the base .sticky-top offset (global.css top:
     var(--safe-area-top)): unlike static, relative positioning still applies
     that offset, which double-counted the status-bar inset — .chat-page's own
     padding-top already reserves the same inset, so the toolbar sat one inset
     lower than the other pages' headers. top:auto restores alignment. */
  .chat-page .sticky-top {
    position: relative;
    top: auto;
  }

  /* Chip + two 44px round buttons on one row: the chip shrinks (ellipsis in
     the value span) instead of wrapping; 44+44 buttons + two 8px gaps */
  .chat-toolbar {
    gap: 8px;
    padding: 8px 0 12px;
  }

  .chat-model-select {
    flex: 0 1 auto;
    min-width: 0;
    /* Phone toolbar row: model capsule + preset picker + 3 icon buttons —
       the capsule leaves room for the preset picker (min 96px) + icons */
    max-width: calc(100% - 220px);
  }

  .chat-model-select :deep(.themed-select__trigger) {
    min-height: 44px;
  }

  /* Preset picker keeps a usable width on the phone band (46px would render
     only the chevron); long names ellipsize inside the trigger */
  .chat-preset-select {
    flex: 0 1 auto;
    min-width: 96px;
    max-width: 44%;
  }

  .chat-preset-select :deep(.themed-select__trigger) {
    min-height: 44px;
  }

  /* 44px touch targets for the round glass buttons */
  .chat-icon-btn {
    width: 44px;
    height: 44px;
  }

  /* Disabled round buttons keep a touch more presence on phone (frame ⑤ .rnd.dis) */
  .chat-icon-btn:disabled {
    opacity: 0.45;
  }

  /* Open-gear state (frame ⑤ .rnd.on): purple glyph + inset accent ring */
  .chat-settings-btn[aria-expanded='true'] {
    color: var(--accent-light);
    border-color: transparent;
    box-shadow: none;
  }

  /* Model chip status dot (frame ⑤ .mchip .dot): green with a soft glow;
     without models a muted gray dot on a muted chip */
  .chat-model-select :deep(.themed-select__trigger)::before {
    background: #10b981;
    box-shadow: none;
  }

  .chat-model-select--empty :deep(.themed-select__trigger) {
    color: var(--text-muted);
  }

  .chat-model-select--empty :deep(.themed-select__trigger)::before {
    background: #b9bfd2;
    box-shadow: none;
  }

  .message-bubble {
    max-width: 88%;
  }

  /* Two-line empty states (frame ⑦ .emptystate): emoji mark + bold title +
     muted sub-caption */
  .empty-hint {
    display: block;
    padding: 60px 20px 0;
    color: var(--text-muted);
  }

  .empty-ico {
    display: block;
    font-size: 40px;
    margin-bottom: 12px;
  }

  .empty-title {
    display: block;
    font-size: 15px;
    font-weight: 700;
    color: var(--text-secondary);
  }

  .empty-sub {
    display: block;
    margin-top: 6px;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-muted);
  }

  /* Composer: 44px touch controls, trimmed band above the bottom tab bar.
     The 10px bottom padding anchors the TaskDock phone offset arithmetic
     (padding-bottom 10 + (row 44 - pill 44) / 2 = 10, see TaskDock.vue's
     media query) — keep both in sync. The Android keyboard avoidance rides
     on top of the 10px anchor (--keyboard-inset from lib/safeArea.ts): the
     composer lifts above the IME while the fixed tab bar stays pinned
     underneath it. */
  .input-area {
    padding: 8px 0 calc(10px + var(--keyboard-inset, 0px));
    position: relative;
  }

  /* Precheck notices float as stacked cards above the composer band (frame ⑦
     .notify): anchored to the input-area's top edge, inside the page's
     symmetric 16px gutters (the phone tier reserves no dock lane). */
  .start-notice-stack {
    position: absolute;
    bottom: calc(100% + 8px);
    left: 0;
    right: 0;
    z-index: 5;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .start-notice {
    flex-wrap: wrap;
    background: var(--bg-card);
    border: none;
    border-radius: var(--r-md);
    padding: 10px 14px;
    box-shadow: var(--shadow-island);
    font-size: 12px;
    font-weight: 600;
    color: var(--text-secondary);
  }

  html[data-theme='dark'] .start-notice {
    background: var(--surface-2);
  }

  .start-notice--error {
    background: var(--danger-bg);
    color: #b91c1c;
  }

  html[data-theme='dark'] .start-notice--error {
    color: #fca5a5;
  }

  .start-notice-btn {
    background: transparent;
    border: none;
    padding: 8px 0 8px 10px;
    font-size: 12px;
    font-weight: 700;
    color: var(--accent-light);
  }

  .start-notice-btn:hover {
    background: transparent;
    text-decoration: underline;
  }

  /* Spinner card (frame ⑦ .spin): 13px ring, purple top arc */
  .start-notice-spinner {
    width: 13px;
    height: 13px;
    border: 2px solid var(--border);
    border-top-color: var(--accent-light);
    animation-duration: 1s;
  }

  /* Degraded composer (frame ⑦): a visible precheck blocker dims the bar */
  .input-row--blocked {
    opacity: 0.6;
  }

  /* Disabled send (frame ⑦): neutral filled circle instead of a ghosted
     gradient — the shape still reads, the affordance clearly gone */
  .send-btn:disabled {
    background: var(--bg-card);
    color: #9aa1b2;
    opacity: 1;
    box-shadow: none;
  }

  /* Streaming polish (frame ⑥): finished assistant bubbles recede while a
     new reply streams; stats/meta numerals go mono; stats line gets the
     dashed hairline; attached images round to 12px */
  .messages-area--streaming .message-row.is-assistant:not(:last-child) .message-bubble {
    opacity: 0.72;
  }

  .stream-meta {
    font-family: var(--font-mono);
  }

  .message-stats {
    font-size: 10.5px;
    margin-top: 8px;
    padding-top: 6px;
    border-top: 1px dashed var(--border);
    font-family: var(--font-mono);
  }

  .message-image {
    border-radius: var(--r-sm);
  }

  /* Thinking block container (frame ⑥ .think): bordered inset card with the
     11/700 state header and 11.5/1.7 muted body */
  .reasoning-block {
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-card);
    padding: 10px 12px;
  }

  html[data-theme='dark'] .reasoning-block {
    background: var(--surface-2);
  }

  .reasoning-header {
    font-size: 11px;
    font-weight: 700;
  }

  .reasoning-body {
    margin-top: 4px;
    font-size: 11.5px;
    line-height: 1.7;
    color: var(--text-muted);
  }

  .attach-btn {
    width: 44px;
    height: 44px;
  }

  .chat-input {
    min-height: 44px;
  }

  .send-btn {
    width: 44px;
    height: 44px;
  }

  .send-btn svg {
    width: 20px;
    height: 20px;
  }
}

/* ─── Tablet (768..1099px): a comfortable centered message column (composer
       included) instead of full-bleed bubbles; the sidebar rail keeps the
       surrounding chrome. No-op above 1099px (desktop keeps the wide layout). ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  .messages-area,
  .input-area {
    width: 100%;
    max-width: 800px;
    margin-left: auto;
    margin-right: auto;
  }
}

/* ─── Tablet portrait Track A (768..1099px; tablet draft frames A⑤/A⑥/A⑦).
   Scoped to the band with min-width: 768px so phones (<=767) and desktop
   (>=1100px) stay untouched. ─── */
@media (min-width: 768px) and (max-width: 1099px) {
  /* Frame A⑤: the params overlay is a CENTERED MODAL CARD (560px) floating on
     the dim backdrop — not the phone's full-width bottom sheet. The class is
     applied only while paramsLayout is 'modal', i.e. exactly this band. */
  .params-sheet--modal {
    left: 50%;
    right: auto;
    bottom: auto;
    top: 90px;
    transform: translateX(-50%);
    width: min(560px, calc(100vw - 48px));
    max-height: calc(100vh - 120px);
    max-height: calc(100dvh - 120px);
    animation: modal-pop 0.2s ease;
  }

  /* The draft's centered card has no grab handle (that is the sheet affordance) */
  .params-sheet--modal .params-grab {
    display: none;
  }

  /* Frame A⑤ .emptystate: emoji mark + bold title + muted sub-caption, like
     the phone tier's two-line empty states */
  .empty-hint {
    display: block;
    padding: 60px 20px 0;
    color: var(--text-muted);
  }

  .empty-ico {
    display: block;
    font-size: 40px;
    margin-bottom: 12px;
  }

  .empty-title {
    display: block;
    font-size: 15px;
    font-weight: 700;
    color: var(--text-secondary);
  }

  .empty-sub {
    display: block;
    margin-top: 6px;
    font-size: 12.5px;
    line-height: 1.6;
    color: var(--text-muted);
  }

  /* Frame A⑥ .think: bordered inset card with the 11/700 state header and the
     11.5/1.7 muted body (same treatment as the phone band) */
  .reasoning-block {
    border: 1px solid var(--border);
    border-radius: var(--r-sm);
    background: var(--bg-card);
    padding: 10px 12px;
  }

  html[data-theme='dark'] .reasoning-block {
    background: var(--surface-2);
  }

  .reasoning-header {
    font-size: 11px;
    font-weight: 700;
  }

  .reasoning-body {
    margin-top: 4px;
    font-size: 11.5px;
    line-height: 1.7;
    color: var(--text-muted);
  }

  /* Frame A⑥: numerals go mono, stats footer gets the dashed hairline */
  .stream-meta {
    font-family: var(--font-mono);
  }

  .message-stats {
    font-size: 10.5px;
    margin-top: 8px;
    padding-top: 6px;
    border-top: 1px dashed var(--border);
    font-family: var(--font-mono);
  }

  /* Frame A⑦ .notify: precheck banner fills the content column as a card */
  .chat-precheck-stack .start-notice {
    background: var(--bg-card);
    border: none;
    border-radius: var(--r-md);
    padding: 10px 14px;
    box-shadow: var(--shadow-island);
    font-size: 12px;
    font-weight: 600;
    color: var(--text-secondary);
  }

  html[data-theme='dark'] .chat-precheck-stack .start-notice {
    background: var(--surface-2);
  }

  .chat-precheck-stack .start-notice--error {
    background: var(--danger-bg);
    color: #b91c1c;
  }

  html[data-theme='dark'] .chat-precheck-stack .start-notice--error {
    color: #fca5a5;
  }

  .chat-precheck-stack .start-notice-btn {
    background: transparent;
    border: none;
    padding: 8px 0 8px 10px;
    font-size: 12px;
    font-weight: 700;
    color: var(--accent-light);
  }

  .chat-precheck-stack .start-notice-btn:hover {
    background: transparent;
    text-decoration: underline;
  }

  /* Frame A⑦ .spin */
  .chat-precheck-stack .start-notice-spinner {
    width: 13px;
    height: 13px;
    border: 2px solid var(--border);
    border-top-color: var(--accent-light);
    animation-duration: 1s;
  }

  /* Frame A⑦: history dims behind the inline banner, composer degrades */
  .messages-area--blocked .message-row {
    opacity: 0.45;
  }

  .input-row--blocked {
    opacity: 0.6;
  }

  /* Frame A⑦ .mchip red dot: precheck blocker turns the chip's status dot red */
  .chat-model-select--down :deep(.themed-select__trigger)::before {
    background: var(--danger);
    box-shadow: none;
  }
}

@keyframes modal-pop {
  from {
    opacity: 0;
    transform: translateX(-50%) scale(0.96);
  }
  to {
    opacity: 1;
    transform: translateX(-50%) scale(1);
  }
}

</style>
