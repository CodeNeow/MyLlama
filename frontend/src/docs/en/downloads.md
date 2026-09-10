The Models page uses a segmented control at the top to switch between its "Download" and "My Models" tabs; the "Download" tab searches online model hubs and fetches `.gguf` model files. Downloaded and imported models all show up together on the "My Models" tab.

## Three download sources

The app supports three model sources, switched via the "Model Download Source" row on the Preferences page; the setting applies to both search and download:

| Source | Notes |
| --- | --- |
| HF Mirror | hf-mirror.com — the first choice on Chinese networks, steady speeds |
| ModelScope | Alibaba's model hub, thorough coverage of Chinese models |
| Hugging Face Official | huggingface.co — the fullest catalog, best on overseas networks |

## Searching and downloading

1. Type a model name or keywords into the pill-shaped search box and press Enter or click "Search".
2. Click a result card to open the **model detail** page:
   - **File list**: every file in the repo, sorted by size with guessed quantization; tick the `.gguf` files you want (multimodal models also need the `mmproj` vision file), then click "Download Selected"; `mmproj` files carry the 👁️ multimodal badge, and when the repo has one you have not ticked yet, a hint above the download bar asks you to select it too — without it a vision model cannot see images;
   - **Description**: the repo's README to help you judge the model.
3. The download task starts immediately and progress updates live — in-progress downloads are pinned to the top of the page as progress cards, and clicking one also opens the download manager.

## Managing download tasks

Click the "Downloads" pill button next to the search box to open the download manager, which has two tabs:

- **Download Tasks**: active and queued tasks, with pause, resume, retry and cancel;
- **Download History**: finished, failed and cancelled records.

Downloads are **resumable**: after pausing or failing, the transfer picks up from where it stopped. Transient network hiccups are retried automatically (up to 3 times). The global task card mirrors download progress on every page. It hugs the bottom-right corner by default and can be dragged to snap to either the left or right edge.

## Importing: reuse models you already have

If GGUF files already exist on this machine, there is no need to re-download:

1. Open the "My Models" tab of the Models page and click "Choose Folder";
2. Pick the directory that holds your models — the app scans it immediately and adds everything it finds to the list.

The "My Models" tab merges models from the **download path** and the **imported path**, labelling each model with its source (Download Path / External Path). Imported directories are scanned including subfolders, and results show each model's architecture, quantization and size.
