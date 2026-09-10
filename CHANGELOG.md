# Changelog

更新日志的**权威来源**（见 `AGENTS.md`「版本发布」）：发版时先在此新增版本条目（含日期），`git tag` 注解消息与 GitHub Release 正文均从该条目复制，保持一致。自 v0.3.3 起条目为概括式双语（中文在上）；v0.3.0 之前的逐提交条目已随对应 tag 与 Release 的清理移除（见文末「历史版本」）。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循语义化版本。

## [v0.5.1] - 2026-09-10

## 中文

v0.5.1 是一个修复版本，解决 v0.5.0 的两个问题：

- **安卓应用内更新修复（重要）** — v0.5.0 的安卓端在应用内点击「立即安装」后，系统安装确认弹窗不会出现，应用闪一下回到首页、更新静默失败。本次重建了安装状态回传链路：修复状态 PendingIntent 的可变标志、补齐确认弹窗的转发逻辑、主界面改为单实例并处理状态重投递——弹窗现已正常弹出，界面不再被重置，安装失败信息也会显示在更新弹窗中。**v0.5.0 安卓用户请在本发布页手动下载 APK 覆盖安装一次**，此后应用内更新恢复正常（安装完成后需手动重新打开应用，属系统行为）。
- **设置页 API Key 状态修复** — API Key 为空时桌面端错误显示「已设置」；现在密钥为空（含纯空白）时，桌面与手机端统一显示「未设置（无鉴权）」，保存时同步按后端规则去除首尾空白。

## English

v0.5.1 is a fix release addressing two v0.5.0 issues:

- **Android in-app update fixed (important)** — on v0.5.0, tapping "Install now" never raised the system confirmation dialog: the app flashed back to the home page and the update silently failed. The install-status handback has been rebuilt (mutable status PendingIntent, confirmation-intent forwarding, single-task MainActivity with status redelivery); the dialog now appears, the UI no longer resets, and install failures surface in the update modal. **Android users on v0.5.0: sideload this release's APK manually once** — in-app updates work again from this version on (after a completed install the app must be reopened manually; that is system behavior).
- **Settings API-key status fixed** — with an empty key the desktop label incorrectly showed "already set"; an empty (or whitespace-only) key now shows "not set (no authentication)" on both desktop and phone, with the input trimmed on save to match the backend's persisted value.

## [v0.5.0] - 2026-09-09

## 中文

v0.5.0:品牌焕新为 **MyLlama**，安卓端正式发布，桌面端全新视觉，推理性能与安全全面加固。核心变化:

**🌟 本版亮点**

- **安卓端正式发布** — 全新手机端界面(底部导航、安全区与软键盘适配、灵动任务胶囊),直连模式单模型常驻、即开即用;一键调优自动按 big.LITTLE 性能核规划线程;支持应用内自更新(侧载分发,arm64)。下载、聊天与模型管理体验与桌面端一致。
- **桌面端全新视觉** — 玻璃拟态 + 紫渐变浮岛设计语言,桌面 / 平板 / 手机三档响应式,浅色 / 深色双主题;首页改为「问候 + 模型状态卡 + 快速上手清单」布局,AI 是否可用一目了然。
- **推理性能细节拉满** — 模型参数页新增 n-gram 投机解码(零额外显存,对代码 / 模板类文本显著加速);支持 q8_0 KV 缓存量化撑起更长上下文;Windows 全量 offload 自动钉定最优线程数并关闭上下文检查点,多花在 CPU 上的每个周期都省下来。

**🔒 安全与稳定**

- **API Key 全链路** — 密钥经环境变量传递,进程列表与启动日志不再可见;聊天、模型管理与模型卸载全部携带鉴权;服务运行中修改密钥会明确提示「重启后生效」并可一键重启。
- **重启竞态修复** — API 页「重启」真正完成 停止 → 等待退出 → 启动,不再只停不起。
- **失效模型自愈** — 聊天页选中的模型被删除 / 改名时自动切换可用模型并轻提示,不再裸报 model not found。

**🪟 Windows 体验**

- **系统托盘** — 关闭窗口最小化到托盘,随时唤回主窗口或退出;无头模式下同样提供托盘图标。
- **安装器品牌迁移** — 全新 MyLlama 安装包自动检测旧版 Llama Desktop:配置、模型库与 llama.cpp 运行时整体迁入新目录并改写记录路径,旧版静默卸载。

**📦 其他**

- Linux 端 GPU 探测扩展:AMD / Intel 等非 NVIDIA 显卡可见并参与一键调优。
- 聊天新增生成预设(精准 / 均衡 / 创意 / 随机 + 自定义另存)。
- 应用内教程支持远程更新(不升级也能获取新章节),手机端新增阅读页。
- 更名 llama-cpp-desktop → **MyLlama**:仓库、安装器与状态文件全部启用新名,旧配置自动迁移;CI 新增安卓构建、模拟器冒烟与真实服务链 e2e。

## English

v0.5.0: the rebrand to **MyLlama**, the Android release, a brand-new desktop look, and across-the-board inference and security hardening. Highlights:

**🌟 Headlines**

- **Android is here** — a brand-new phone UI (bottom navigation, safe-area & soft-keyboard handling, floating task capsule), direct-mode serving with a single resident model that's ready out of the box, auto-tune capped to big.LITTLE performance cores, and in-app self-updates (sideload, arm64). Downloads, chat and model management match the desktop experience.
- **A brand-new desktop look** — glassmorphism and violet-gradient floating islands across three responsive tiers (desktop / tablet / phone) with light & dark themes; the Home page now leads with a greeting, a model status card and a quick-start checklist.
- **Inference performance, tuned to the last detail** — n-gram speculative decoding (zero extra VRAM, a real speed-up for code / template-style text), q8_0 KV-cache quantization for longer contexts, and Windows full-offload plans that pin the optimal thread count and disable context checkpoints.

**🔒 Security & stability**

- **API key end to end** — the key is delivered via an environment variable, invisible in process lists and startup logs; chat, model listing and unload all carry bearer auth; saving a key while the service runs offers an explicit one-click restart.
- **Restart race fixed** — the API page restart truly completes stop → wait → start.
- **Stale model self-heal** — a deleted or renamed selected model auto-switches with a notice instead of a raw "model not found".

**🪟 Windows experience**

- **System tray** — closing the window minimizes to the tray; headless mode gets a tray icon too.
- **Installer migration** — the MyLlama installer detects a legacy Llama Desktop install, migrates config, model library and the llama.cpp runtime into the new directory (rewriting recorded paths) and silently uninstalls the old product.

**📦 Also**

- Linux GPU probing extended: AMD / Intel and other non-NVIDIA cards are now visible to one-click tuning.
- Chat gains generation presets (precise / balanced / creative / random + custom).
- The in-app tutorial updates remotely (new sections without upgrading) and gains a phone reader page.
- Renamed llama-cpp-desktop → **MyLlama** across repo, installer and state files (legacy configs migrate automatically); CI adds Android build + emulator smoke and real-service e2e tests.

## [v0.3.9] - 2026-08-30

## 中文

v0.3.9:生成速度折线图回归与跨平台地基。核心变化:

- **生成速度折线图回归** — API 路由页 Token 速度卡恢复「近 60 秒生成速度」曲线,每秒采样、滚动保留 60 个点;模型管理页标题下补回副标题行,与其他页面版式一致。
- **应用路径按操作系统规范化** — Windows 行为完全不变;macOS/Linux 下配置、缓存与默认模型 / llama.cpp 目录归入用户配置目录(旧配置自动迁移),不再依赖进程工作目录,为 Linux/macOS 支持铺路。
- **响应式与平台地基** — 新增移动 / 平板 / 桌面三档断点体系与平台状态分类器(本版不改变任何现有界面),为适配手机、平板等不同尺寸做准备。

## English

v0.3.9: the decode-speed chart returns, plus cross-platform groundwork. Highlights:

- **Decode-speed chart restored** — the Token speed card on the API Router page regains the "decode speed over last 60s" line chart (1s sampling, rolling 60 points); the Models page gets its subtitle line back, matching every other page.
- **Per-OS app path resolution** — Windows behavior is unchanged; on macOS/Linux the config, caches and default model / llama.cpp directories move under the per-user config directory (legacy config migrates automatically) instead of relying on the process working directory, paving the way for Linux/macOS support.
- **Responsive and platform foundation** — a three-tier breakpoint system (mobile / tablet / desktop) and a platform-state classifier land without changing any existing UI, preparing for phone- and tablet-sized screens.

## [v0.3.8] - 2026-08-29

## 中文

v0.3.8:推理显卡选择、一键调优实测校准与实时预览、服务日志工程、无头控制面与内置帮助。核心变化:

- **推理显卡选择** — 设置页可指定服务运行在哪张显卡(按 nvidia-smi UUID 经 CUDA_VISIBLE_DEVICES 钉定);多卡机器上一键调优按所选显卡规划参数,规划卡与运行卡不再脱节。
- **调优实测校准** — 一键调优先实测本机内存带宽(按硬件指纹缓存、跨机自动作废),在「全量 offload 上下文局促」与「cpu-moe 大上下文」之间自动优选;模型设置页新增「深度实测」,用自带 llama-bench 实测当前方案的真实解码速度。
- **一键调优移入参数设置页** — 调优结果实时填充表单,不再是列表页一闪而过的摘要。
- **CUDA 兼容性三态显示** — 首页 CUDA 兼容性区分「兼容 / 需 CUDA ≥12.8 / 已满足」,Blackwell 显卡装好达标运行时后不再误报警告。
- **服务日志工程** — llama-server 日志落盘文件并跟踪:GUI↔无头交接后日志不再丢失;ANSI 剥离与进度行节流(修复 Windows `\r\r\n` 幽灵空行);容量 200→2000 行;界面按游标增量拉取。
- **无头模式控制面** — API 路由模式下新增 `127.0.0.1:1900` 控制面(健康 / 状态 / 日志 / 停止):破坏性端点仅回环可达,可选 token 鉴权,绑定失败自动降级不影响启动。
- **优雅停止** — 停止服务先中断、宽限 5 秒后才强杀(Windows 行为不变);被交接的服务仍按原方式停止。
- **交接收养加固** — GUI↔无头切换认领服务前校验进程创建时间,防止 PID 复用导致的误认领与误杀。
- **崩溃安全写入** — 配置与交接文件原子写入(临时文件 + fsync + 原子换名),断电不再产生撕裂文件;版本三处一致性校验加入质量门。
- **内置帮助文档** — 侧栏新增「帮助」页:9 章节中英双语教程(快速上手 / 运行时 / 下载 / 模型 / API / 聊天 / 设置 / 无头模式 / 常见问题)。

## English

v0.3.8: serving-GPU selection, measured auto-tune calibration with live preview, service-log engineering, a headless control plane and an in-app help center. Highlights:

- **Serving-GPU selection** — pick the GPU the service runs on in Settings (pinned via CUDA_VISIBLE_DEVICES by stable nvidia-smi UUID); the auto-tuner plans against the selected card on multi-GPU hosts.
- **Measured tune calibration** — one-click tuning measures the machine's RAM bandwidth first (cached per hardware fingerprint, rejected across machines) and picks between a cramped full-offload and a large-context cpu-moe plan; Model Settings gains "Deep benchmark" measuring the real decode speed of the saved plan via the bundled llama-bench.
- **Auto-tune lives in Model Settings** — tuning fills the settings form in real time instead of flashing a summary on the list.
- **Three-state CUDA compatibility** — the Home CUDA row distinguishes Compatible / Requires CUDA ≥12.8 / Satisfied, so Blackwell cards with a qualifying runtime stop raising false alarms.
- **Service-log engineering** — llama-server logs go to a file and are tailed: logs survive GUI↔headless handover; ANSI is stripped and progress redraws throttled (fixing Windows `\r\r\n` phantom rows); the ring grew to 2000 lines with cursor-based incremental fetch.
- **Headless control plane** — API-route mode gains a 127.0.0.1:1900 control plane (health / status / logs / stop): destructive endpoints are loopback-enforced with optional token auth; bind failure degrades startup-free.
- **Graceful stop** — stopping the service interrupts first and escalates to kill after a 5 s grace (Windows unchanged); handed-over servers keep their existing stop path.
- **Handover adoption hardening** — adoption verifies the process creation time, defending against PID reuse (no more adopting or killing an unrelated process).
- **Crash-safe writes** — config and handover files are written atomically (temp + fsync + atomic rename); a version-triple sync check joined the quality gate.
- **In-app documentation** — the sidebar gains a Help page: nine bilingual tutorial sections (quickstart / runtime / downloads / models / api / chat / settings / headless / faq).

## [v0.3.7] - 2026-08-27

## 中文

v0.3.7:快速上手引导、官方 Hugging Face 下载源、可选 API Key 与无障碍修复。核心变化:

- **快速上手清单** — 首页新增三步引导卡(安装运行时 → 下载模型 → 启动服务),全部完成或手动关闭后不再显示。
- **官方 Hugging Face 下载源** — 模型下载源在 HF Mirror、ModelScope 之外新增「Hugging Face 官方」选项。
- **ModelScope 修复** — 修复 ModelScope 搜索与下载不可用的问题。
- **可选 API Key** — 设置页「服务访问范围」新增可选 API Key:非空时 llama-server 以 `--api-key` 启动,调用推理接口需携带 Bearer Token(无凭据请求返回 401),留空保持无认证;局域网暴露时可防止同网段无凭据调用。
- **下拉框键盘导航** — 全部下拉(模型设置页推理参数 + 聊天模型选择)支持键盘操作:方向键移动高亮、Enter 选择、Esc 关闭、Home/End 跳转,修复键盘与读屏用户无法操作下拉的问题。
- **模型说明渲染** — 下载详情页「模型说明」不再原样显示 README 中的 HTML/Markdown 标记,改为剥离内嵌 HTML 后渲染。
- **系统信息修正** — 修正 GPU Compute Capability 识别与系统信息缓存探测的问题。
- **启动与界面细节** — 补应用图标消除启动时 favicon.ico 404;首页卡片排版统一;API 页监控与 Token 布局修正。

## English

v0.3.7: quick-start onboarding, official Hugging Face download source, optional API key and accessibility fixes. Highlights:

- **Quick-start checklist** — the Home page gains a three-step onboarding card (install the runtime → download a model → start the service) that hides once every step is done or the user dismisses it.
- **Official Hugging Face source** — model downloads gain an "official Hugging Face" option alongside HF Mirror and ModelScope.
- **ModelScope fixed** — ModelScope search and downloads are usable again.
- **Optional API key** — the Settings access-scope section gains an optional API key: when set, llama-server starts with `--api-key` and inference requests must carry the bearer token (unauthenticated requests get 401); leaving it empty keeps today's no-authentication behavior — useful to stop credential-free calls when the service is exposed to the LAN.
- **Dropdown keyboard navigation** — all dropdowns (ModelSettings inference params + the chat model picker) are fully keyboard-operable: arrow keys move the highlight, Enter selects, Escape closes, Home/End jump — fixing keyboard and screen-reader accessibility.
- **Model description rendering** — the download detail page no longer shows raw HTML/Markdown source in the description; embedded HTML is stripped and the remainder is rendered.
- **System info fixes** — corrected GPU compute capability detection and cached system-info probing.
- **Startup & UI polish** — added the app icon to remove the per-startup favicon.ico 404; Home card typography unified; API page monitor and token layout fixed.

## [v0.3.6] - 2026-08-25

## 中文

v0.3.6:系统信息页增强与布局优化。核心变化:

- **GPU 显存使用率** — 系统信息页的 GPU 卡片新增显存已用/总量、使用率百分比与进度条，方便评估当前显存占用。
- **Compute Capability 显示** — GPU 卡片显示 NVIDIA 计算能力版本，Blackwell 架构（Compute Cap 12.x）自动提示需要 CUDA 12.8+。
- **Toolkit 版本显示优化** — CUDA Toolkit 未安装（无 nvcc）时不再显示"未安装"造成误导，改为隐藏该卡片（驱动已足够推理使用）。
- **磁盘空间信息** — 新增磁盘使用率卡片，显示下载路径所在卷的总容量、已用和可用空间。
- **首页布局优化** — 移除首页内部滚动条，改用弹性换行布局，卡片自动两列等高排列，窗口自适应。

## English

v0.3.6: System info page enhancements and layout improvements. Highlights:

- **GPU VRAM usage** — the GPU card on the system info page now shows used/total memory, usage percentage with a progress bar, helping assess current VRAM consumption.
- **Compute Capability display** — the GPU card shows NVIDIA compute capability version; Blackwell architecture (Compute Cap 12.x) gets an automatic hint requiring CUDA 12.8+.
- **Toolkit version display improved** — when CUDA Toolkit is not installed (no nvcc), the card is hidden instead of showing "Not installed" to avoid confusion (the driver alone is sufficient for inference).
- **Disk space info** — a new disk usage card shows total, used, and free space for the volume containing the download path.
- **Home page layout improved** — removed the internal scrollbar from the Home page; cards now flow in a flex-wrap two-column equal-height layout that adapts to window size.

## [v0.3.5] - 2026-08-23

## 中文

v0.3.5:API 路由页布局重构、llama.cpp 下载修复与下载管理分页。核心变化:

- **llama.cpp 下载修复** — 适配 llama.cpp 新发布策略(二进制只随每夜 prerelease 发布,latest 仅剩标记文件):自动回退到 release 列表,选取最新带平台构建的每夜版本下载。
- **下载取消修复** — 元数据获取与传输请求全部绑定取消上下文,点击「取消」立即中断在途请求;修复「暂停后取消」状态卡死在 paused、取消与继续按钮双双失灵的问题。
- **下载管理分页** — 下载任务与下载历史拆成两个页签,完成的历史不再混入任务列表;弹窗固定高度、切换页签尺寸不变,长列表内部滚动、页签吸顶。
- **按钮用词统一** — llama.cpp 下载的「停止」改为「取消」,与模型下载、应用更新一致。
- **API 路由页布局** — 宽度上限提至 1920px,全屏几乎无左右留白;大窗口下一个参数一行、间距随窗口增大,监控区离底悬浮不再贴边;矮窗口自动紧凑模式(处理器/内存并排、隐藏次要说明),最小窗口回到 900×600 仍完整无滚动。

## English

v0.3.5: API Router layout rework, llama.cpp download fixes and a tabbed download manager. Highlights:

- **llama.cpp download fixed** — adapts to llama.cpp's new release strategy (binaries ship only in nightly prereleases while /releases/latest is an asset-less marker): the app falls back to the release list and downloads the newest nightly carrying a platform build.
- **Download cancel fixed** — release-metadata fetches and transfer requests are all bound to the cancel context, so Cancel aborts in-flight requests immediately; also fixed stop-from-paused stranding the state machine in paused with Cancel and Resume both dead.
- **Tabbed download manager** — download tasks and download history are separate tabs, so finished downloads no longer mix into the task list; the modal keeps a fixed height across tab switches, long lists scroll internally with a sticky tab bar.
- **Consistent wording** — the llama.cpp download stop button now reads Cancel, matching model downloads and app updates.
- **API Router layout** — page width cap raised to 1920px (nearly no side margins when maximized); tall windows show one metric per row with spacing that grows with the window and the monitor area floating above the bottom edge; short windows switch to a compact mode (CPU + memory side by side, secondary captions hidden) so the 900x600 minimum stays fully visible without scrolling.

## [v0.3.4] - 2026-08-22

## 中文

v0.3.4:API 路由页监控区精简与固定布局。核心变化:

- **移除 Token 速度折线图** — API 路由页 Token 卡只保留提示词处理速度与生成速度两个数值指标,近 60 秒生成速度历史曲线及图表辅助代码一并删除。
- **监控列固定无滚动** — 系统监控与 Token 速度两张卡始终完整填满右列,不再出现内部滚动条;启动 / 停止服务时卡片高度保持不变,未启动时仅显示占位提示。
- **页面底部收紧** — 监控网格与窗口底部的间距从约 80px 收窄到 24px,日志控制台与监控区可视高度增加。
- **下载搜索示例更新** — 模型下载页搜索框占位示例改为 LLM 模型(如 Qwen3.8-27B、Qwen3.6-35B-A3B)。
- **窗口最小尺寸提高** — 主窗口最小尺寸从 900×600 提高到 900×800,与默认高度一致,矮窗口不再裁剪监控区内容。
- **安装路径记忆修复** — 归属信息变更使安装器卸载注册表键名改变,覆盖安装丢失自定义路径回落默认目录;现在按「当前键 → v0.3.x 旧键 → llama-gui 旧键」顺序读回上次安装路径,并清理被取代的旧键避免重复条目。

## English

v0.3.4: streamlined, fixed-layout monitor on the API Router page. Highlights:

- **Token speed chart removed** — the Token card keeps only the two numeric metrics (prompt processing / generation speed); the 60-second decode-speed history chart and its helpers are gone.
- **Scroll-free monitor column** — the System monitor and Token speed cards always fill the right column completely with no internal scrollbar; card heights stay identical whether the service is running or stopped, with only the placeholder shown before start.
- **Tighter bottom spacing** — the monitor grid ends 24px above the window bottom (was ~80px), giving the log console and monitor more visible height.
- **Download search examples** — the model download page's search placeholder now shows LLM examples (e.g. Qwen3.8-27B, Qwen3.6-35B-A3B).
- **Larger minimum window** — the main window's minimum size rises from 900×600 to 900×800 (matching the default height), so short windows no longer clip the monitor cards.
- **Install path memory fixed** — an attribution-driven rename of the uninstall registry key made updates lose the remembered custom path and fall back to the default directory; the installer now reads the previous InstallLocation from the current key, then the v0.3.x-era key, then the pre-rename llama-gui key, and removes the superseded entry.

## [v0.3.3] - 2026-08-22

## 中文

v0.3.3:模型 ID 直接复制可用、系统信息页健壮性加固、文档中文化。核心变化:

- **模型 ID 所见即所得** — API 的 `model` 字段就是页面显示的模型名,从模型管理页、API 路由页或聊天页直接复制粘贴即可调用(替代 v0.3.2 的大写 ID 方案);仅大小写冲突的模型名自动加后缀区分。聊天页之前保存的模型需重新选择一次。
- **健壮性** — 系统信息页的内存/显存格式化对缺失数据防御,显示 N/A 而非整页卡在加载骨架。
- **应用内更新日志** — 解析兼容新的双语日志格式(中文在上、英文在下),历史格式不受影响。
- **文档** — README 以中文为主体(英文版移至 README_en.md),移除 Linux/Ubuntu 相关内容,双语言界面截图全部重拍(含新增的运行环境页);发布规则改为概括式双语更新日志。

## English

v0.3.3: copy-paste model IDs, hardened System Info formatters, and Chinese-primary documentation. Highlights:

- **Copy-paste model IDs** — the API `model` field is now exactly the name shown in the UI; copy it from the Model Manager, API Router or Chat page and it just works (replacing v0.3.2's uppercase scheme), with automatic suffixes only for case-insensitive collisions. A chat model selected under v0.3.2 needs re-picking once.
- **Robustness** — System Info memory/VRAM formatting is null-safe: missing data renders N/A instead of stalling the whole page on the loading skeleton.
- **In-app release notes** — extraction handles the new bilingual format (Chinese first, English second); historical bodies parse unchanged.
- **Docs** — README is now Chinese-primary (English moved to README_en.md), Linux/Ubuntu content removed, both screenshot sets recaptured (including the new Runtime Environment page); changelog rules switched to concise bilingual summaries.

## [v0.3.2] - 2026-08-22

## English

v0.3.2: fixed-viewport pages with internal scrolling, chat markdown rendering with live reasoning scroll, uppercase model IDs, and the Hf_Model display-name fix (5 commits since v0.3.1, per-commit core changes):

1. `3a9c0cc` fix(models): reject Hf_Model placeholder names and unify preset aliases to uppercase
   - Converter placeholder values in GGUF general.name ("Hf_Model", case-insensitive, alongside "Unsloth_Gguf") are rejected, and a "<model>-GGUF" variant-directory fallback now yields to the more specific main file name — the barozp/Qwen3.6-29B-REAP model displays its real name instead of "Hf_Model". Preset INI section aliases are uppercased so the model id served by GET /models, the OpenAI API, the chat picker and TaskDock is one uniform uppercase id (llama-server matches ids case-sensitively).
2. `835e2fe` fix(frontend): pin chat input to the bottom, live-scroll reasoning, render markdown
   - Chat input no longer scrolls away with the conversation (flex min-height fix); the streaming thinking block pins to its newest text and never yanks a user who scrolled up; assistant messages render markdown (markdown-it, html escaped as injection defense, breaks, linkify) with theme-aware styles; user input stays plain text. New deps: markdown-it, @types/markdown-it.
3. `2606c3a` fix(frontend): fixed-viewport pages with internal scrolling on Home/Runtime/Chat/API
   - The four main pages never scroll as a page; only inner bands scroll (cards region, log console, monitor column, messages). Root-cause fix for the TaskDock bottom reserve breaking the fixed layout: routes carry meta.fixed and .content-area drops its dock reserve for them. The API page keeps its original card design — monitor grid fills the remaining viewport height and the right column scrolls internally with both cards reachable.
4. `735bbce` chore(frontend): sync wailsjs bindings for TuneModelConfig
   - Checked-in Wails bindings regenerated for the TuneModelConfig method added in e9cbf29 (drift-only diff).
5. `chore(release)` add v0.3.2 changelog entry and bump version
   - core/VERSION realigned with the release tags: it had been stale at v0.2.9 since v0.3.0, which made every v0.3.x build report v0.2.9 internally and offer itself a permanent false "update available" prompt.

## 中文

v0.3.2:页面固定布局与内部滚动、聊天 Markdown 渲染与思考实时滚动、模型 ID 统一大写、Hf_Model 显示名修复(v0.3.1 以来 5 个提交,按提交逐一说明核心改动):

1. `3a9c0cc` fix(models): 拒绝 Hf_Model 占位名并统一预设别名为大写
   - GGUF general.name 中的转换工具占位名("Hf_Model",大小写不敏感,连同既有 "Unsloth_Gguf")被拒绝显示;"<模型>-GGUF" 目录回退名让位于更具体的主文件名——barozp/Qwen3.6-29B-REAP 显示真实名称而非 "Hf_Model"。预设 INI 节名统一大写,GET /models、OpenAI API、聊天选择器与 TaskDock 使用同一个大写模型 ID(llama-server 按大小写精确匹配)。
2. `835e2fe` fix(frontend): 聊天输入框固定底部、思考过程实时滚动、渲染 Markdown
   - 输入框不再随对话滚走(flex min-height 修复);流式思考块实时钉在最新文字,用户上滚回看时绝不强行拽回;助手消息按 Markdown 渲染(markdown-it,原始 HTML 转义防注入、单换行、链接识别),明暗主题适配;用户输入保持纯文本。新增依赖 markdown-it。
3. `2606c3a` fix(frontend): 系统信息/运行环境/本地聊天/API 路由四页固定视口、内容内部滚动
   - 四个主页面整页不再滚动,仅内部区域滚动(卡片区、日志控制台、监控右列、消息区)。修复 TaskDock 底部预留破坏固定布局的根因:路由携带 meta.fixed,content-area 对固定页去掉预留。API 页保持原版卡片设计——监控网格占满剩余视口高度,右列内部滚动、两卡均可触达。
4. `735bbce` chore(frontend): 同步 TuneModelConfig 的 wailsjs 绑定
   - 重新生成 Wails 绑定,补上 e9cbf29 新增的 TuneModelConfig 方法(纯漂移修正)。
5. `chore(release)` 新增 v0.3.2 更新日志并提升版本号
   - core/VERSION 与发布 tag 重新对齐:自 v0.3.0 起滞留在 v0.2.9,导致所有 v0.3.x 构建对内报告 v0.2.9、永久误报"有新版本"。

## [v0.3.1] - 2026-08-21

## English

v0.3.1: hardware-aware one-click auto-tune (MoE cpu-moe plans, 128k contexts), model display-name fixes, paused Ubuntu builds, and the GPL v3 license switch (7 commits since v0.3.0, per-commit core changes):

1. `488b8e9` docs: sync architecture table and READMEs with the current codebase
   - AGENTS.md: overview mentions API-route (headless) mode; architecture table adds core/headless.go, core/handover.go and the singleinstance / headlessalert / installer_launch / diskusage / devbuild / locale platform pairs; the views row adds Runtime plus the sidebar order; the lib row adds selectOptions.ts / dockSpace.ts. READMEs move the llama.cpp status and download entry point from System Info to the Runtime Environment page and document the apiRouteMode and download-path config fields; CONTRIBUTING bumps Go to 1.25+.
2. `ffe6132` fix(models): trim repo-id prefix from GGUF metadata model names
   - buildModelInfo trims the "org/" segment when general.name embeds the full source repo id (e.g. "cerebras/GLM-4.7-Flash-REAP-23B-A3B" written by the converter), so the Models page shows only the model name; preset aliases shorten accordingly. Test: TestScanModelsDirGGUFMetaRepoIDName.
3. `5ef2010` fix(models): prefer the file name when the GGUF metadata name is only its prefix
   - Unsloth-style converters write the bare base-model name ("Qwen3.5-9B") into general.name for every quant variant in a repo; when the resolved name is a proper prefix of the main file name at a "-"/"_" boundary, the more specific file name is displayed instead (Qwen3.5-9B-UD-Q4_K_XL). Generic file names ("model.gguf") never qualify; the chat model picker follows via preset aliases. Tests: TestScanModelsDirGGUFMetaBaseNamePrefix (prefix + non-separator boundary).
4. `e9cbf29` feat(models): one-click hardware-aware auto-tune for per-model inference params
   - New core/autotune.go: readGGUFModelMetrics parses the real GGUF header (block count, GQA/MLA/hybrid-attention KV geometry, trained context) with a correct GGUF v3 array skipper (u64 counts); the pure deterministic tuneModelConfig planner picks gpuLayers / ctxSize / threads / flashAttn / cache types against the hardware snapshot (full offload f16 → q8_0 upgrade on NVIDIA → partial offload bounded by VRAM and RAM → CPU-only). New binding TuneModelConfig persists through SaveModelConfig validation; the Models page gains a per-card sparkle button with inline bilingual feedback. 12 new focused tests incl. preset-integration.
5. `5a5b0d6` fix(models): MoE-aware auto-tune (cpu-moe plan), 128k context ladder, icon spacing
   - readGGUFTensorSplit walks the tensor info table for an exact MoE-expert vs dense byte split (GLM-23B-A3B: 89.2% expert); new cpu-moe decision step keeps experts in system RAM and dense weights + KV on the GPU with auto threads — measured 26.7 t/s at ctx 131072 on RTX 5070 vs 22.8 t/s for the previous partial-offload plan; context ladder extended to 131072 with a 0.92 budget cap on ≥65536 tiers (tight 128k plans OOM at load); Models card action icons grouped adjacently (two auto margins previously split the row).
6. `44309b3` chore(build): temporarily disable the Ubuntu build and release artifacts
   - CI build-linux (three .deb packages) gets if: false until Ubuntu support lands; the release job drops build-linux from needs and validates 1 artifact (Windows exe) instead of 4; frontend / backend check jobs unchanged.
7. `2d1b20d` docs: switch the project license from MIT to GPL v3
   - LICENSE replaced with the canonical GNU GPL v3 text (gnu.org); README badges and license sections updated in both languages; third-party dependency license metadata and test fixtures untouched.

## 中文

v0.3.1:硬件感知的一键调优(MoE cpu-moe 方案、128k 上下文)、模型显示名修复、暂停 Ubuntu 构建、切换 GPL v3 协议(v0.3.0 以来 7 个提交,按提交逐一说明核心改动):

1. `488b8e9` docs: 同步架构表与 README 至当前代码
   - AGENTS.md:概览补充 API 路由(无头)模式;架构表新增 core/headless.go、core/handover.go 及 singleinstance / headlessalert / installer_launch / diskusage / devbuild / locale 平台文件对;视图行补充 Runtime 页与侧边栏顺序;lib 行补充 selectOptions.ts / dockSpace.ts。README 将 llama.cpp 状态与下载入口从系统信息页移至运行环境页,并补记 apiRouteMode 与下载路径配置字段;CONTRIBUTING 的 Go 版本升至 1.25+。
2. `ffe6132` fix(models): 裁剪 GGUF 元数据模型名中的仓库 ID 前缀
   - buildModelInfo 在 general.name 内嵌完整源仓库 ID(如转换工具写入的 "cerebras/GLM-4.7-Flash-REAP-23B-A3B")时裁掉 "org/" 段,模型管理页只显示模型名;预设别名同步变短。测试:TestScanModelsDirGGUFMetaRepoIDName。
3. `5ef2010` fix(models): 元数据名仅为文件名前缀时优先显示文件名
   - unsloth 类转换工具对仓库内所有量化变体写入同一个基础模型名("Qwen3.5-9B");当解析出的名字是主文件名在 "-"/"_" 边界处的严格前缀时,改显更具体的文件名(Qwen3.5-9B-UD-Q4_K_XL)。通用文件名("model.gguf")不受影响;聊天选择器的模型 ID 经预设别名同步。测试:TestScanModelsDirGGUFMetaBaseNamePrefix(前缀与无分隔符边界)。
4. `e9cbf29` feat(models): 按硬件一键调优逐模型推理参数
   - 新增 core/autotune.go:readGGUFModelMetrics 解析真实 GGUF 头(层数、GQA/MLA/混合注意力 KV 几何、训练上下文),数组跳过器按 GGUF v3 实际的 u64 计数实现;纯函数 tuneModelConfig 依据硬件快照规划 gpuLayers / ctxSize / threads / flashAttn / 缓存类型(全量卸载 f16 → NVIDIA 上 q8_0 升级 → 受显存与内存双重约束的部分卸载 → 纯 CPU)。新绑定 TuneModelConfig 经 SaveModelConfig 校验持久化;模型管理页每卡新增 ✨ 按钮与双语内联反馈。新增 12 个聚焦测试(含预设集成)。
5. `5a5b0d6` fix(models): MoE 感知调优(cpu-moe 方案)、128k 上下文阶梯、图标间距
   - readGGUFTensorSplit 遍历张量信息表得到专家/稠密字节的精确拆分(GLM-23B-A3B 专家占 89.2%);新增 cpu-moe 决策步:专家留内存、稠密权重 + KV 上 GPU、线程自动——RTX 5070 实测 26.7 t/s、上下文 131072,优于旧部分卸载方案的 22.8 t/s;上下文阶梯扩至 131072 并对 ≥65536 档位施加 0.92 预算上限(贴边的 128k 计划会在加载时显存不足);模型卡片操作图标改为紧邻分组(此前两个 auto 边距把图标分开)。
6. `44309b3` chore(build): 暂时屏蔽 Ubuntu 构建与发布产物
   - CI 的 build-linux(三个 .deb 包)加 if: false,待 Ubuntu 适配完成后恢复;release 任务从 needs 移除 build-linux,产物数量校验从 4 改为 1(仅 Windows exe);frontend / backend 检查任务不变。
7. `2d1b20d` docs: 项目协议从 MIT 切换为 GPL v3
   - LICENSE 替换为 gnu.org 的 GPL v3 权威文本;两个 README 的徽章与协议章节双语更新;第三方依赖的许可证元数据与测试夹具未动。

## [v0.3.0] - 2026-08-19

## English

v0.3.0: Runtime Environment page rename, llama.cpp component-level status, sidebar reordering, and stop button fix (4 commits since v0.2.9, per-commit core changes):

1. `bd46b08` feat(app): rename the page to Runtime Environment and split llama.cpp into per-component status
   - Core: renamed `/libraries` route and view to `/runtime` (name: `Runtime`, meta title '运行环境', layers icon retained). Added `LlamaCppInfo.cudartInstalled` (bool, JSON field, backward compatible) and `detectCudartRuntime(dir string) bool` (glob for cudart*.dll / cublas*.dll on Windows, false on other platforms). No new bindings; wails.ts unchanged; wailsjs regenerated on build. Frontend: new pure helpers in lib/llamaDownload.ts: `isCudartAsset(name)` and `packageRows(fileName, downloaded, total, mainBytes)` returning done/active/progress for main program and CUDA runtime, omitting cudart row if absent to avoid false Vulkan/CPU build waiting lines. Runtime.vue uses `watch(fileName)` to snapshot `mainBytes` when switching to cudart package, then cudart progress = `(Downloaded-mainBytes)/(Total-mainBytes)` clamped 0-100. Extraction stage shows "Extracting" on the active row. Pause/Resume/Stop/Retry logic unchanged. Component keys: `runtime.compMain`, `runtime.compCudart`, `runtime.compCudartDesc`, `runtime.pkgMain` / `runtime.pkgCudart`, status badge styles retained. Tests: backend `TestDetectCudartRuntime` (temp dir with cudart64_12.dll → true; cublas64_12.dll → true; only llama-server.exe → false; empty dir → false). Frontend: llamaDownload.test.ts adds `isCudartAsset` / `packageRows` cases (single package, two packages in progress, two packages main done, divide-by-zero protection), i18n test block renamed to runtime keys + new component keys bilingual assertions.

2. `9533d90` fix(app): stop button should reset paused state to idle, and move Downloads after Runtime
   - Core: `core/engine.go` lines ~1515-1529: stop handler previously called `cancel()` but didn't reset status from "paused" to "idle", so the UI kept showing "Resume" button. Fixed condition: `if downloadState.Status != "paused" && downloadState.Status != "idle"` allows reset. Frontend: no changes (DownloadButton.vue handles pause/stop via `stopDownload()` → `downloadCancel()` → context cancellation → status reset logic).

3. `a83d07f` feat(frontend): reorder sidebar navigation to place API route above Chat
   - Frontend: `Sidebar.vue` navItems array: API route (`/api`) moved before Chat (`/chat`), keeping Downloads, Models, API, Settings in original order. New order: Home → Runtime → Downloads → API → Chat → Models → Settings.

4. `08f1188` refactor(frontend): reorder sidebar navigation for better user flow
   - Frontend: Reordered navItems to follow natural user workflow: Chat page moved before Downloads, API route moved after Models (service start before chat). New sidebar order: Home → Runtime → Chat → Downloads → Models → API → Settings. Rationale: User workflow is check system status → run environment → chat → download models → manage models → start service → configure.

## 中文

v0.3.0: 运行环境页重命名、llama.cpp 组件级状态、侧边栏重排与停止按钮修复（v0.2.9 以来 4 个提交，按提交逐一说明核心改动）：

1. `bd46b08` feat(app): 重命名页面为运行环境并拆分 llama.cpp 为组件级状态
   - 核心：路由 `/libraries` 与视图重命名为 `/runtime`（name: `Runtime`，meta title '运行环境'，layers 图标保留）。新增 `LlamaCppInfo.cudartInstalled`（bool，JSON 字段，向后兼容）与 `detectCudartRuntime(dir string) bool`（Windows 下 glob cudart*.dll / cublas*.dll，其他平台 false）。无新绑定；wails.ts 不变；wailsjs 由构建自动再生成。前端：lib/llamaDownload.ts 新增纯函数 `isCudartAsset(name)` 与 `packageRows(fileName, downloaded, total, mainBytes)` 返回主程序和 CUDA 运行时的 done/active/progress，若 cudart 不存在则不显示等待行。Runtime.vue 用 `watch(fileName)` 切换到 cudart 包时快照 `mainBytes`，此后 cudart 进度 = `(Downloaded-mainBytes)/(Total-mainBytes)` 钳 0-100。解压阶段在当前活动行显示「正在解压」。暂停/继续/停止/重试逻辑不变。组件键：`runtime.compMain`、`runtime.compCudart`、`runtime.compCudartDesc`、`runtime.pkgMain` / `runtime.pkgCudart`，状态徽标样式保留。测试：后端 `TestDetectCudartRuntime`（临时目录含 cudart64_12.dll → true；含 cublas64_12.dll → true；仅 llama-server.exe → false；空目录 → false）。前端：llamaDownload.test.ts 新增 `isCudartAsset` / `packageRows` 用例（单包、双包进行中、双包主包完成、除零保护），i18n 测试块重命名为 runtime 键 + 新组件键双语断言。

2. `9533d90` fix(app): 停止按钮应重置暂停状态为 idle，并将 Downloads 移动到 Runtime 之后
   - 核心：`core/engine.go` 行 ~1515-1529：停止处理器此前调用 `cancel()` 但未重置状态从 "paused" 到 "idle"，UI 仍显示「Resume」按钮。修复条件：`if downloadState.Status != "paused" && downloadState.Status != "idle"`，允许重置。前端：无改动（DownloadButton.vue 通过 `stopDownload()` → `downloadCancel()` → context cancellation → status reset 处理暂停/停止逻辑）。

3. `a83d07f` feat(frontend): 重排侧边栏导航将 API 路由置于 Chat 之前
   - 前端：`Sidebar.vue` navItems 数组：API 路由（`/api`）移动到 Chat（`/chat`）之前，保持 Downloads、Models、API、Settings 原有序列。新顺序：Home → Runtime → Downloads → API → Chat → Models → Settings。

4. `08f1188` refactor(frontend): 重排侧边栏导航以符合更好的用户流程
   - 前端：reordered navItems 以遵循自然用户流程：Chat 页移动到 Downloads 之前，API 路由移动到 Models 之后（服务启动在聊天之前）。新侧边栏顺序：Home → Runtime → Chat → Downloads → Models → API → Settings。理由：用户流程为检查系统状态 → 运行环境 → 对话 → 下载模型 → 管理模型 → 启动服务 → 配置。

## 历史版本（v0.2.x 及更早）

v0.1.6 – v0.2.9 的逐提交发布记录已随对应 tag 与 GitHub Release 的清理移除（v0.2.4 从未发布），变更明细可经 git 提交历史追溯。v0.2.0 为项目更名 llama-desktop 后的首个发布（更名重置发布历史时 v0.1.x 的 tag 与 Release 已被删除）。
