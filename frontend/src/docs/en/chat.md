The "Local Chat" page is a ready-to-use chat interface talking straight to your local model — no data ever leaves the machine.

## Starting a chat

1. Pick a model in the capsule at the top (clicking it opens the list of models recognized on this machine — the service does not need to be running yet);
2. Type a message into the floating input bar — **Enter sends, Shift+Enter makes a new line** — or hit the round gradient send button;
3. If the service is not running yet, sending **starts it automatically** and loads the selected model on demand before streaming begins — no trip to the "API Router" page needed (that page still offers manual start/stop and monitoring);
4. Replies stream in token by token; while generating, the send button turns into a red stop button you can hit to interrupt; when a reply finishes, its generation speed (tok/s) is shown under the message.

Before the auto-start, a guided check runs: with no usable models you are pointed to the Models page to download or import one; if the llama.cpp runtime is missing you are pointed to the "Runtime Environment" tab of the System Environment page. Fill the gap and you are ready to chat.

Sending a message first unloads every OTHER loaded model so the selected one is the only one in memory; load / unload changes show up in the task dock in real time, and models can be unloaded from there at any moment. The dock hugs the bottom-right corner by default and can be dragged to snap to either the left or right edge.

> Android note: the official Android llama-server runs in **direct mode** — one service process hosts exactly one model. When the selected model is not the resident one, sending a message automatically restarts the service to load it (first launch is slower on phone storage, please be patient); the task dock and the Home page's resident-model card both offer an unload button — in direct mode unloading means stopping the service, which frees the memory, and the service restarts automatically on the next message. Desktop platforms keep router mode with on-demand loading and unloading of multiple models, unchanged.

Multimodal models (with an mmproj file) can also take images: use the paperclip button next to the input bar to attach one and let the model describe it.

A vision model needs **two files**: the main weights `.gguf` and the `mmproj` vision projector `.gguf`. If the selected model has no projector (no mmproj file next to the weights and no explicit projector path in the model settings), the chat page disables the attach button; pasting or picking an image is dropped with a hint; sending with images attached is blocked with guidance — re-open the model's detail page under Models → Download and grab the mmproj file carrying the 👁️ multimodal badge, or set the projector path in the model settings.

## LAN remote chat (phone to PC)

The "Chat target" switcher at the top of the toolbar toggles between **This PC** and **Remote PC**. On the remote tier, chat requests go straight to a llama-server already running on another computer in the same network: the model list, unloading other models and streaming all act on that machine — no models or runtime are needed on this device.

**PC side (serving) — three steps** (see the "Preferences" section):

1. Under "Settings → Server Access Scope", switch the service to **LAN (0.0.0.0)** (applies the next time the service starts);
2. Set a key under "Settings → API Key" (strongly recommended: any device on the network can find the service port);
3. Open "Settings → LAN Pairing → Share this machine's service": on desktop it shows the **pairing QR code** (this machine's address, port and API key) — scanning it with another MyLlama device imports the whole pairing; "Copy link" hands it over through the clipboard instead, or copy the address, port and key individually for manual entry.

**Phone side (chatting) — three steps**:

1. Under "Settings → LAN Pairing → Connect to another PC", tap "**Scan to import**" (Android) and aim at the pairing QR on the PC to fill in the address, port and API key automatically — the first use asks for the **camera permission**, granted once and not asked again; without a camera (or while working on the PC itself), tap "**Import from clipboard**" and paste the `myllama://pair` link copied on the PC, or enter the PC address (the host or IP only, e.g. `192.168.1.5`), the port and the API key set on the PC manually;
2. Review the imported fields, then turn on **Enable remote chat** and save (importing only fills the form — nothing saves by itself);
3. Back on the chat page, switch "Chat target" to **Remote PC** — the model list becomes the PC's models; just type and stream.

On the remote tier the PC manages its own service: the chat page never starts or stops it, and a failed connection surfaces a hint to check the address, port and firewall. Every behavior of the "This PC" tier stays unchanged.

> Security boundary: LAN traffic is **plaintext HTTP**. The API key keeps unrelated devices from freeloading the service but **does not prevent eavesdropping** within the same network — enable remote chat on trusted networks only.

## Tuning chat parameters

Sampling parameters are owned by the **sampling preset** picker next to the model capsule in the top toolbar, borrowed from unsloth's generation presets: **Default** (no override — the per-model sampling parameters saved in the model settings apply), **Precise** (temperature 0.3 / top_p 0.8 / top_k 20 / repeat_penalty 1.1), **Balanced** (0.7 / 0.9 / 40 / 1.1), **Creative** (1.0 / 0.95 / 60 / 1.05) and **Random** (1.2 / 1.0 / 80 / 1.0). With any non-default preset selected, the sent request body carries those four sampling override fields; with "Default" none are attached and the server-side parameters decide. The selection is remembered across sessions.

After picking a non-default preset, the bookmark icon in the toolbar opens "Manage presets": **save the current preset under a custom name** (up to 20) and delete custom presets you no longer need.

Click the gear icon at the right end of the top toolbar to open the parameter panel:

- **Sampling preset summary**: a read-only line at the top showing the active preset and its four values;
- **Max tokens**: the length cap for a single reply;
- **System prompt**: persona or task instructions for the model; left empty, nothing is injected.

"Reset defaults" restores max tokens and the system prompt. Changes apply from the next request on.

## Thinking process

Reasoning models (e.g. the DeepSeek-R1 series) emit a thinking phase; the chat page folds it into a "Thinking" block you can expand, keeping only the final answer in the body — the two phases get separate speed readouts.

## Chat history

- "Clear chat" deletes the current conversation;
- Conversations are stored locally (browser local storage); closing and reopening the app keeps your last conversation.

Every request goes to the local service — nothing is uploaded to any cloud.
