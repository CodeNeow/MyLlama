package core

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── llama-quantize (desktop GGUF quantization tool) ────────────
// Quantizes a GGUF model in place next to its source file by shelling out to
// llama-quantize (shipped in the same llama.cpp release package as
// llama-server; located through the same directory chain). One task at a
// time: a second start while running is rejected. The child's stdout+stderr
// stream into a dedicated ring buffer (the same logfmt line assembler the
// server log uses, so \r progress redraws coalesce) surfaced to the frontend
// via GetQuantizeStatus.

// quantizeTypes is the accepted quantization vocabulary — the same four
// choices the unsloth export flow offers (GGUF_QUANTS = [q4_k_m, q5_k_m,
// q8_0, f16]); llama-quantize itself parses the type case-insensitively
// (tools/quantize/quantize.cpp try_parse_ftype uppercases the input).
var quantizeTypes = map[string]bool{
	"q4_k_m": true,
	"q5_k_m": true,
	"q8_0":   true,
	"f16":    true,
}

// quantizeBin is the llama.cpp tool binary this feature drives (findLlamaBinInDir
// appends .exe on Windows, mirroring llama-server's lookup).
const quantizeBin = "llama-quantize"

// quantLogsCap caps the quantize log ring (llama-quantize prints per-tensor
// progress; a few hundred lines cover the interesting tail of any run).
const quantLogsCap = 500

// QuantizeLogEntry is one llama-quantize output line in the ring snapshot.
type QuantizeLogEntry struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

// QuantizeStatus is the full task state returned by GetQuantizeStatus: the
// running flag, the terminal result of the most recent task and a snapshot of
// the recent log lines. The frontend polls it while the quantize dialog is open.
type QuantizeStatus struct {
	Running bool               `json:"running"`
	Done    bool               `json:"done"`    // a task finished since process start (success or failure)
	Success bool               `json:"success"` // meaningful when Done
	Error   string             `json:"error"`   // failure / cancel reason (Done && !Success)
	SrcPath string             `json:"srcPath"`
	OutPath string             `json:"outPath"`
	Quant   string             `json:"quant"`
	Logs    []QuantizeLogEntry `json:"logs"` // most recent lines (ring snapshot)
	Next    int64              `json:"next"` // seq the next log line will receive
}

// quantize state, all guarded by quantizeMu:
//   - running:  a child llama-quantize is alive (single-flight gate)
//   - done/success/error: terminal state of the most recent task
//   - cancel:   kill handle of the running child (nil when idle)
//   - last task identity (src/out/quant) so the status reports what ran
var (
	quantizeMu      sync.Mutex
	quantizeRunning bool
	quantizeDone    bool
	quantizeSuccess bool
	quantizeError   string
	quantizeCancel  func()
	quantizeSrc     string
	quantizeOut     string
	quantizeType    string
)

// quantLogs ring (guarded by its own mutex so the reader goroutine never holds
// quantizeMu while a poll drains the snapshot).
var (
	quantLogsMu  sync.Mutex
	quantLogs    []QuantizeLogEntry
	quantLogsSeq int64
)

// quantAddLog appends one assembled line to the ring, evicting beyond the cap.
func quantAddLog(text string) {
	quantLogsMu.Lock()
	quantLogs = append(quantLogs, QuantizeLogEntry{Seq: quantLogsSeq, Text: text})
	quantLogsSeq++
	if len(quantLogs) > quantLogsCap {
		quantLogs = quantLogs[len(quantLogs)-quantLogsCap:]
	}
	quantLogsMu.Unlock()
}

// resolveQuantizeBin locates llama-quantize through the same directory chain as
// resolveLlamaServerBin (llama.cpp download dir → imported custom dir → PATH).
// Declared as a var so tests can pin the lookup without a real install.
var resolveQuantizeBin = func() string {
	if p := findLlamaBinInDir(llamaCppDownloadDir(), quantizeBin); p != "" {
		return p
	}
	customLlamaCppMu.Lock()
	customDir := customLlamaCppDir
	customLlamaCppMu.Unlock()
	if customDir != "" {
		if p := findLlamaBinInDir(customDir, quantizeBin); p != "" {
			return p
		}
	}
	if _, err := exec.LookPath(quantizeBin); err == nil {
		return quantizeBin
	}
	return ""
}

// validQuantizeType reports whether quant is one of the four supported types.
// Strict allowlist match (no trimming): the value comes straight from the
// frontend radio buttons, and a padded value is garbage worth rejecting.
func validQuantizeType(quant string) bool {
	return quantizeTypes[quant]
}

// validQuantOutName validates the user-editable output file name: a trimmed
// plain file name (no directory separators — the output always lands next to
// the source) with a .gguf suffix (appended when the user omitted it).
func validQuantOutName(name string) (string, bool) {
	// Surrounding whitespace in the ORIGINAL input is rejected (the INI-free
	// argv path does not trim it away; a padded name would create " x.gguf").
	if name != strings.TrimSpace(name) {
		return "", false
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." || len(trimmed) > 255 {
		return "", false
	}
	if trimmed != filepath.Base(trimmed) {
		return "", false
	}
	if strings.ContainsAny(trimmed, "\n\r\x00") {
		return "", false
	}
	if !strings.EqualFold(filepath.Ext(trimmed), ".gguf") {
		trimmed += ".gguf"
	}
	return trimmed, true
}

// buildQuantizeArgs assembles the llama-quantize command line: positional
// <input> <output> <type> (quantize.cpp reads argv[arg_idx] as input, tries the
// next token as the ftype and otherwise takes an explicit output in between —
// the three-positional form selects the explicit-output path).
func buildQuantizeArgs(src, out, quant string) []string {
	return []string{src, out, strings.ToLower(strings.TrimSpace(quant))}
}

// StartQuantize launches one llama-quantize run writing outName next to
// srcPath. Single-flight: a second start while a task is running is rejected.
// Validation failures (unknown type, missing source, existing output, missing
// binary) return before any process is spawned.
func StartQuantize(srcPath, quant, outName string) error {
	// Platform gate: the Android package ships only llama-server (no
	// llama-quantize binary and no exec-friendly filesystem layout for it).
	if platformGOOS == "android" {
		return errors.New(tr("Android 构建不包含量化工具", "the quantization tool is not available on the Android build"))
	}
	if !validQuantizeType(quant) {
		return fmt.Errorf(tr("非法量化类型 %q：仅支持 q4_k_m / q5_k_m / q8_0 / f16", "invalid quantization type %q: only q4_k_m / q5_k_m / q8_0 / f16 are supported"), quant)
	}
	if strings.TrimSpace(srcPath) == "" {
		return errors.New(tr("源文件路径不能为空", "source file path cannot be empty"))
	}
	src := filepath.Clean(srcPath)
	if !filepath.IsAbs(src) {
		return fmt.Errorf(tr("源文件必须是绝对路径: %s", "source file must be an absolute path: %s"), src)
	}
	fi, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf(tr("源文件不存在: %s", "source file not found: %s"), src)
	}
	if fi.IsDir() {
		return fmt.Errorf(tr("源路径是目录而非文件: %s", "source path is a directory, not a file: %s"), src)
	}
	if !strings.EqualFold(filepath.Ext(src), ".gguf") {
		return fmt.Errorf(tr("源文件必须是 .gguf 文件: %s", "source file must be a .gguf file: %s"), src)
	}
	outName, ok := validQuantOutName(outName)
	if !ok {
		return fmt.Errorf(tr("非法输出文件名 %q", "invalid output file name %q"), outName)
	}
	outPath := filepath.Join(filepath.Dir(src), outName)
	if _, err := os.Stat(outPath); err == nil {
		return fmt.Errorf(tr("输出文件已存在，请更换文件名: %s", "output file already exists, pick another name: %s"), outPath)
	}

	quantizeMu.Lock()
	if quantizeRunning {
		quantizeMu.Unlock()
		return errors.New(tr("已有量化任务正在运行，请先等待完成或取消", "a quantization task is already running; wait for it to finish or cancel it"))
	}
	bin := resolveQuantizeBin()
	if bin == "" {
		quantizeMu.Unlock()
		return errors.New(tr("未找到 llama-quantize：请安装完整的 llama.cpp 发行包（量化工具与 llama-server 同目录）", "llama-quantize not found: install the full llama.cpp release package (the tool ships next to llama-server)"))
	}

	// Fresh task state: clear the previous result and the log ring so the
	// dialog's tail shows only this run.
	quantizeRunning = true
	quantizeDone = false
	quantizeSuccess = false
	quantizeError = ""
	quantizeSrc = src
	quantizeOut = outPath
	quantizeType = strings.ToLower(strings.TrimSpace(quant))
	quantLogsMu.Lock()
	quantLogs = nil
	quantLogsMu.Unlock()

	// Shared stdout+stderr pipe: one write end bound to both fds gives the
	// same "one file description" interleaving guarantee as the server log
	// file capture (bridge.go) — concurrent writes can never split a line.
	pr, pw, pipeErr := os.Pipe()
	if pipeErr != nil {
		quantizeRunning = false
		quantizeMu.Unlock()
		return fmt.Errorf(tr("创建量化日志管道失败: %w", "failed to create the quantize log pipe: %w"), pipeErr)
	}
	cmd := exec.Command(bin, buildQuantizeArgs(src, outPath, quantizeType)...)
	hideWindow(cmd)
	cmd.Stdout = pw
	cmd.Stderr = pw

	addServerLog(fmt.Sprintf("[INFO] Starting llama-quantize: %s %s", bin, strings.Join(cmd.Args[1:], " ")))
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		quantizeRunning = false
		quantizeMu.Unlock()
		quantAddLog("[ERROR] " + err.Error())
		return fmt.Errorf(tr("启动 llama-quantize 失败: %w", "failed to start llama-quantize: %w"), err)
	}

	// Cancel handle: killing the child is the only lever (there is no graceful
	// stop protocol with llama-quantize); the wait goroutine reports the kill
	// as a failed run.
	cancel := func() {
		_ = cmd.Process.Kill()
	}
	quantizeCancel = cancel
	quantizeMu.Unlock()

	// Reader goroutine: the sole owner of the pipe reader and the line
	// assembler (same logfmt assembly as the server log tailer). Completed
	// lines append immediately; "\r" progress-redraw partials are throttled to
	// 500 ms so llama-quantize's per-tensor progress bar stays live without
	// flooding the ring. Exits at EOF, i.e. when the child closed its fds.
	go func() {
		defer pr.Close()
		buf := make([]byte, 16384)
		asm := lineAssembler{}
		var lastPartial time.Time
		appendPiece := func(piece logPiece, force bool) {
			clean := strings.TrimSpace(stripANSI(piece.Text))
			if clean == "" {
				return
			}
			if piece.Kind == piecePartial && !force {
				now := time.Now()
				if now.Sub(lastPartial) < 500*time.Millisecond {
					return
				}
				lastPartial = now
			}
			quantAddLog(clean)
		}
		for {
			n, readErr := pr.Read(buf)
			if n > 0 {
				for _, piece := range asm.Feed(string(buf[:n])) {
					appendPiece(piece, piece.Kind == pieceLine)
				}
			}
			if readErr != nil {
				break
			}
		}
		for _, piece := range asm.Flush() {
			appendPiece(piece, true)
		}
	}()

	// Wait goroutine: publishes the terminal state and releases the slot.
	go func() {
		waitErr := cmd.Wait()
		pw.Close() // the child is gone; drop our write end so the reader hits EOF

		quantizeMu.Lock()
		quantizeRunning = false
		quantizeCancel = nil
		quantizeDone = true
		if waitErr == nil {
			quantizeSuccess = true
			quantizeError = ""
			quantizeMu.Unlock()
			quantAddLog("[OK] done")
			log.Println("[OK] llama-quantize finished:", quantizeOut)
			return
		}
		quantizeSuccess = false
		if _, ok := waitErr.(*exec.ExitError); ok {
			// Killed by CancelQuantize or a non-zero tool exit; the log tail
			// carries the tool's own error message either way.
			quantizeError = tr("llama-quantize 非正常退出（可能已取消，详见日志）", "llama-quantize exited abnormally (possibly cancelled, see the log)")
		} else {
			quantizeError = waitErr.Error()
		}
		quantizeMu.Unlock()
		log.Println("[WARN] llama-quantize failed:", waitErr)
	}()
	return nil
}

// CancelQuantize kills the running llama-quantize child. Idempotent per state:
// cancelling while idle is a no-op error so the UI cannot mistake it for a
// successful cancel.
func CancelQuantize() error {
	quantizeMu.Lock()
	defer quantizeMu.Unlock()
	if !quantizeRunning || quantizeCancel == nil {
		return errors.New(tr("当前没有正在运行的量化任务", "no quantization task is running"))
	}
	quantizeCancel()
	return nil
}

// GetQuantizeStatus snapshots the current task state and the recent log tail.
func GetQuantizeStatus() QuantizeStatus {
	quantizeMu.Lock()
	st := QuantizeStatus{
		Running: quantizeRunning,
		Done:    quantizeDone,
		Success: quantizeSuccess,
		Error:   quantizeError,
		SrcPath: quantizeSrc,
		OutPath: quantizeOut,
		Quant:   quantizeType,
	}
	quantizeMu.Unlock()
	quantLogsMu.Lock()
	st.Logs = make([]QuantizeLogEntry, len(quantLogs))
	copy(st.Logs, quantLogs)
	st.Next = quantLogsSeq
	quantLogsMu.Unlock()
	return st
}
