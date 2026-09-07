The "My Models" tab of the Models page gathers every usable model on this machine, and each model can have its own inference parameters. When the API service starts, the app turns these parameters into a llama-server preset per model.

## The model list

The list merges two sources, distinguished by a label:

- **Download Path**: models downloaded through the Download tab;
- **External Path**: directories imported via "Choose Folder".

Each model card shows the name, architecture (e.g. `qwen2`), quantization (e.g. `Q4_K_M`), size and full path. Multimodal-capable models carry a "Multimodal" badge (an mmproj file was found next to them). Click "Refresh" to rescan after files change.

## Local quantization (desktop only)

In "My Models", **right-click a model card** and pick "Quantize…" to convert the model into a smaller quantized variant with llama.cpp's own llama-quantize tool: the target supports the q4_k_m / q5_k_m / q8_0 / f16 levels (q4_k_m is the best size/quality balance, f16 is lossless), the output is auto-named `<original>-<quant>.gguf` (editable) and lands in the source file's directory. While running, the dialog shows a live log and offers a cancel; when finished, the new file appears next to the source and "My Models" refreshes automatically.

This feature is desktop-only (the quantize tool ships in the full llama.cpp release package next to llama-server; the Android package does not include it). Note that re-quantizing an already-quantized model requires a high-precision source — otherwise llama-quantize reports the error in the log.

## Opening model settings

Click the gear icon on a model card to open that model's dedicated settings page. Parameters are organized into seven tabs — Basic / LoRA Adapters / Inference / Memory-Loading / Multi-GPU / Long Context / Advanced — each with its own usage hint.

The "LoRA Adapters" tab manages the LoRA mounts **specific to this model**: drop LoRA adapter GGUFs trained and exported by Unsloth and friends into the "model download dir/lora" folder and click "Rescan" — every file is listed with its size and Alpha, and its GGUF metadata is checked to tell real adapters apart from ordinary models. Flip the switch, set the weight (scale, 0–4, default 1.0) and click "Save Adapters"; the adapters then mount onto this model via `--lora-scaled` when the service starts (each model carries its own set; an adapter must match its base model's architecture to load). While the service is running, "Apply to Running Service" hot-applies the saved weights: Android direct mode applies them immediately, while on desktop the pinned llama.cpp router cannot accept runtime updates and the page answers with the "takes effect on the next service start" hint — just restart the service.

## One-click auto-tune

The "Auto-tune" button sits at the top right of the settings page; you do not need to understand any parameter to use it:

1. Click it once — the app reads the model GGUF's real metrics (layer count, attention head structure, KV cache geometry, trained context, MoE expert sizes) plus a hardware snapshot (GPU VRAM, RAM, CPU cores, measured memory bandwidth) and computes an optimal parameter set;
2. The result **fills the form in real time** — every field (GPU layers, context size, cache type, CPU threads, ...) updates immediately so you can review and hand-tweak anything before saving;
3. Click "Save Settings" when satisfied.

Tuning plans VRAM against the **inference GPU** chosen in Preferences: on multi-GPU machines, whichever card you picked is the one whose VRAM is budgeted. Possible plans include full GPU offload (fastest), cpu-moe for MoE models (experts stay in RAM, the rest goes to the GPU), partial offload (fallback when VRAM runs out) and CPU-only (no GPU), with the largest context that fits the budget. On Apple Silicon (macOS arm64) the Metal plan keeps every layer GPU-resident via unified memory and sizes the context from the RAM budget (Flash Attention stays off for now); on Linux, AMD / Intel GPUs are GPU-accelerated through the Vulkan build too.

## Quick parameter reference

| Parameter | Meaning and advice |
| --- | --- |
| GPU Layers | `auto` puts everything into VRAM (recommended); a number does partial offload when VRAM is tight; `0` for CPU-only inference |
| Context Size | how much text the model "sees" at once; larger for longer conversations, limited by VRAM |
| CPU Threads | `-1` for auto; lower it on weak CPUs or when heat/usage is high |
| Flash Attention | Speeds up inference and saves VRAM; recommended with a GPU |
| cpu-moe | For MoE models (DeepSeek, Qwen3-MoE, ...) low on VRAM: keep expert layers in RAM |
| KV Cache Type | `q8_0` is nearly lossless and saves VRAM (recommended); default is f16 |
| Load Mode | Default mmap loads fastest; mlock prevents swapping when RAM is ample |
| Split Mode | `none` for a single GPU; `layer` is the stable default for multi-GPU |

> Note: Android is a CPU-only build, so GPU-only parameters (GPU layers, Flash Attention, cpu-moe) are not shown there; the `dio` load mode is only offered on Windows / Linux. Android detects the SoC model automatically (e.g. Snapdragon / Dimensity, read from system properties); on Android, auto-tune caps the thread count at the performance-core count (big.LITTLE efficiency cores do not join inference threads), and the tune-result toast for the CPU-only plan no longer shows a GPU-layers field.

## Changing parameters while the service is running

Presets are generated **when the service starts**. If the service is running, saving shows a note that "parameters take effect the next time the service starts" — restart the service on the "API Router" page to apply them.
