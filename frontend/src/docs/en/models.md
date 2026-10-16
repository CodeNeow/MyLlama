The "My Models" tab of the Models page gathers every usable model on this machine, and each model can have its own inference parameters. When the API service starts, the app turns these parameters into a llama-server preset per model.

## The model list

The list merges two sources, distinguished by a label:

- **Download Path**: models downloaded through the Download tab;
- **External Path**: directories imported via "Choose Folder".

Each model card shows the name, architecture (e.g. `qwen2`), quantization (e.g. `Q4_K_M`), size and full path. Multimodal-capable models carry a "Multimodal" badge (an mmproj file was found next to them). Click "Refresh" to rescan after files change.

## Opening model settings

Click the gear icon on a model card to open that model's dedicated settings page. Parameters are organized into six tabs — Basic / Inference / Memory-Loading / Multi-GPU / Long Context / Advanced — each with its own usage hint.

## One-click auto-tune

The "Auto-tune" button sits at the top right of the settings page; you do not need to understand any parameter to use it:

1. Click it once — the app reads the model GGUF's real metrics (layer count, attention head structure, KV cache geometry, trained context, MoE expert sizes) plus a hardware snapshot (GPU VRAM, RAM, CPU cores, measured memory bandwidth) and computes an optimal parameter set;
2. The result **fills the form in real time** — every field (GPU layers, context size, cache type, CPU threads, ...) updates immediately so you can review and hand-tweak anything before saving;
3. Click "Save Settings" when satisfied.

Tuning plans VRAM against the **inference GPU** chosen in Preferences: on multi-GPU machines, whichever card you picked is the one whose VRAM is budgeted. Possible plans include full GPU offload (fastest), cpu-moe for MoE models (experts stay in RAM, the rest goes to the GPU), partial offload (fallback when VRAM runs out) and CPU-only (no GPU), with the largest context that fits the budget. On Apple Silicon (macOS arm64) the Metal plan keeps every layer GPU-resident via unified memory, sizes the context from the RAM budget and leaves Flash Attention to llama-server's automatic choice (measured no slower than forcing it on); on hybrid chips (Apple Silicon, Android SoCs) the thread count is capped at the performance-core count; on Linux, AMD / Intel GPUs are GPU-accelerated through the Vulkan build too.

## Quick parameter reference

| Parameter | Meaning and advice |
| --- | --- |
| GPU Layers | `auto` puts everything into VRAM (recommended); a number does partial offload when VRAM is tight; `0` for CPU-only inference |
| Context Size | how much text the model "sees" at once; larger for longer conversations, limited by VRAM |
| CPU Threads | `-1` for auto; lower it on weak CPUs or when heat/usage is high. On hybrid chips (Apple Silicon, Android SoCs) auto-tune only uses the performance cores — measured M2 4P+4E CPU-only: 21.5 t/s at 4 threads vs 10.0 at 8 |
| Flash Attention | Checking it forces `-fa on`, leaving it unchecked lets llama-server choose — measured M2 Metal decode: auto 30.0 ≈ forced on 29.7, off 27.2 t/s |
| cpu-moe | For MoE models (DeepSeek, Qwen3-MoE, ...) low on VRAM: keep expert layers in RAM |
| KV Cache Type | `q8_0` nearly halves the cache size (longer contexts fit), at a slight decode cost — measured M2 Metal: 28.4 vs 30.1 t/s on f16; pick it when memory is tight; default is f16 |
| Load Mode | Default mmap loads fastest; mlock prevents swapping when RAM is ample |
| Split Mode | `none` for a single GPU; `layer` is the stable default for multi-GPU |
| Speculative Decoding | "Advanced" tab. `ngram-simple` / `ngram-mod` are n-gram self-speculation: no draft model, zero extra VRAM; `draft-mtp` is MTP speculative prediction and needs model support; empty disables. Measured M2 Metal 4B Q4_K_M: on highly repetitive output `ngram-mod` is ~2.8x (86-88 vs 31 t/s baseline), on natural prose it gains nothing, and `ngram-simple` costs ~5% either way — enable it for code / templated output only |

> Note: Android is a CPU-only build, so GPU-only parameters (GPU layers, Flash Attention, cpu-moe) are not shown there; the `dio` load mode is only offered on Windows / Linux. Android detects the SoC model automatically (e.g. Snapdragon / Dimensity, read from system properties); on Android and Apple Silicon, auto-tune caps the thread count at the performance-core count (big.LITTLE efficiency cores and Apple Silicon E-cores do not join inference threads), and the tune-result toast for the CPU-only plan no longer shows a GPU-layers field.

## Changing parameters while the service is running

Presets are generated **when the service starts**. If the service is running, saving shows a note that "parameters take effect the next time the service starts" — restart the service on the "API Router" page to apply them.
