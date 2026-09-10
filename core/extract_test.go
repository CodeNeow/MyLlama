package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExtractZipFileTooLarge verifies extractZip rejects entries exceeding the per-file limit.
// The test lowers maxExtractFileSize to 16 bytes and builds a zip fixture with a 100-byte file,
// asserting an error is returned and the target file is not written (#2 extraction bomb per-file limit).
// The error string is returned via tr in the current language; fixed zh ensures the assertion is stable.
func TestExtractZipFileTooLarge(t *testing.T) {
	setLanguageForTest(t, "zh")
	orig := maxExtractFileSize
	maxExtractFileSize = 16
	defer func() { maxExtractFileSize = orig }()

	zipPath := filepath.Join(t.TempDir(), "big.zip")
	if err := writeTestZip(zipPath, map[string]string{"big.bin": strings.Repeat("A", 100)}); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	err := extractZip(zipPath, dest)
	if err == nil {
		t.Fatal("超过单文件上限的 zip 应返回错误")
	}
	if !strings.Contains(err.Error(), "超出解压大小上限") {
		t.Errorf("错误信息应说明大小超限: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "big.bin")); statErr == nil {
		t.Error("超限文件不应被写出到磁盘")
	}
}

// TestExtractZipTotalTooLarge verifies extractZip returns an error when the cumulative extraction size exceeds the limit
// (#2 extraction bomb total-size limit). Two small files each stay under the per-file limit, but together trigger totalBytes > maxExtractTotalSize.
func TestExtractZipTotalTooLarge(t *testing.T) {
	origFile, origTotal := maxExtractFileSize, maxExtractTotalSize
	maxExtractFileSize = 16
	maxExtractTotalSize = 25
	defer func() {
		maxExtractFileSize = origFile
		maxExtractTotalSize = origTotal
	}()

	zipPath := filepath.Join(t.TempDir(), "total.zip")
	if err := writeTestZip(zipPath, map[string]string{
		"a.bin": strings.Repeat("A", 15),
		"b.bin": strings.Repeat("B", 15),
	}); err != nil {
		t.Fatal(err)
	}

	if err := extractZip(zipPath, t.TempDir()); err == nil {
		t.Error("累计总大小超限的 zip 应返回错误")
	}
}

// TestExtractZipOK verifies an under-limit zip extracts normally (control case for #2: the limit must not reject valid small files).
func TestExtractZipOK(t *testing.T) {
	orig := maxExtractFileSize
	maxExtractFileSize = 16
	defer func() { maxExtractFileSize = orig }()

	zipPath := filepath.Join(t.TempDir(), "small.zip")
	if err := writeTestZip(zipPath, map[string]string{"ok.bin": strings.Repeat("A", 10)}); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("小文件 zip 不应报错: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "ok.bin"))
	if err != nil {
		t.Fatalf("解压产物缺失: %v", err)
	}
	if len(data) != 10 {
		t.Errorf("文件内容长度 = %d, want 10", len(data))
	}
}

// TestExtractTarGzSymlinkRejected verifies extractTarGz returns an error for symlink entries (#6).
// The default branch only handles directories and regular files; unknown types like symlinks must be explicitly rejected
// rather than silently skipped, otherwise extracted content may be incomplete or cause accidental writes.
func TestExtractTarGzSymlinkRejected(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "link.tar.gz")
	if err := writeTestTarGz(tarPath, []tarFixture{
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Size: 0},
	}); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(tarPath, t.TempDir()); err == nil {
		t.Error("含 symlink 条目的 tar.gz 应返回错误")
	}
}

// TestExtractTarGzFileTooLarge verifies extractTarGz rejects entries exceeding the per-file limit (#2).
// When the tar header declares a size above the limit, extraction is blocked before any disk write.
func TestExtractTarGzFileTooLarge(t *testing.T) {
	orig := maxExtractFileSize
	maxExtractFileSize = 16
	defer func() { maxExtractFileSize = orig }()

	tarPath := filepath.Join(t.TempDir(), "big.tar.gz")
	if err := writeTestTarGz(tarPath, []tarFixture{
		{Name: "big.bin", Typeflag: tar.TypeReg, Size: 100},
	}); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(tarPath, t.TempDir()); err == nil {
		t.Error("超过单文件上限的 tar.gz 应返回错误")
	}
}

// writeTestZip builds a zip archive in memory containing the given files and writes it to zipPath.
func writeTestZip(zipPath string, files map[string]string) error {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			return err
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.WriteFile(zipPath, buf.Bytes(), 0644)
}

// zipFixture describes a zip file entry: content plus an explicit unix mode.
type zipFixture struct {
	Content string
	Mode    os.FileMode
}

// writeTestZipWithModes builds a zip archive whose entries carry explicit unix
// mode bits (via FileHeader.SetMode, i.e. the creator-unix external attributes
// a real Unix-created zip carries) and writes it to zipPath.
func writeTestZipWithModes(zipPath string, files map[string]zipFixture) error {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, f := range files {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(f.Mode)
		fw, err := w.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := fw.Write([]byte(f.Content)); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.WriteFile(zipPath, buf.Bytes(), 0644)
}

// tarFixture describes an entry to write into a tar.gz archive. Mode 0 means
// the historical 0644 default; non-zero modes are written verbatim so exec-bit
// preservation can be exercised.
type tarFixture struct {
	Name     string
	Typeflag byte
	Linkname string
	Size     int64
	Mode     int64
}

// writeTestTarGz builds a tar.gz archive in memory containing the given fixtures and writes it to tarPath.
func writeTestTarGz(tarPath string, fixtures []tarFixture) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range fixtures {
		mode := f.Mode
		if mode == 0 {
			mode = 0644
		}
		hdr := &tar.Header{Name: f.Name, Typeflag: f.Typeflag, Linkname: f.Linkname, Size: f.Size, Mode: mode}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if f.Size > 0 {
			if _, err := tw.Write(make([]byte, f.Size)); err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(tarPath, buf.Bytes(), 0644)
}

// ─── Exec-bit preservation (Unix) ────────────────────────────────

// TestExtractTarGzPreservesExecMode verifies extractTarGz applies the tar
// header's permission bits to regular files: the upstream llama.cpp Android /
// Linux / macOS assets are .tar.gz whose bin/ entries carry 0755 — without
// mode application the extracted llama-server is 0644 and cannot exec.
// Windows does not track exec permission bits, so the assertion is skipped
// there.
func TestExtractTarGzPreservesExecMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not track unix exec permission bits")
	}
	tarPath := filepath.Join(t.TempDir(), "llama.tar.gz")
	if err := writeTestTarGz(tarPath, []tarFixture{
		{Name: "llama-b9999/bin/llama-server", Typeflag: tar.TypeReg, Size: 4, Mode: 0755},
	}); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := extractTarGz(tarPath, dest); err != nil {
		t.Fatalf("extractTarGz failed: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dest, "llama-b9999", "bin", "llama-server"))
	if err != nil {
		t.Fatalf("extracted binary missing: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0755 {
		t.Errorf("extracted tar entry mode = %v, want 0755 (exec bit must survive extraction)", got)
	}
}

// TestExtractZipPreservesUnixExecMode verifies extractZip applies the
// creator-unix mode bits when present: a zip entry carrying 0755 must extract
// executable (fixes phase-2 linux/macOS downloads of zipped binaries).
// Windows has no exec bit, so the assertion is skipped there.
func TestExtractZipPreservesUnixExecMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not track unix exec permission bits")
	}
	zipPath := filepath.Join(t.TempDir(), "llama.zip")
	if err := writeTestZipWithModes(zipPath, map[string]zipFixture{
		"llama-server": {Content: "ELF...", Mode: 0755},
	}); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("extractZip failed: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dest, "llama-server"))
	if err != nil {
		t.Fatalf("extracted binary missing: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0755 {
		t.Errorf("extracted zip entry mode = %v, want 0755 (creator-unix exec bit must survive extraction)", got)
	}
}

// TestExtractZipDefaultsTo0644WhenModeAbsent verifies the fallback: an entry
// whose creator-unix attributes carry no usable mode (Mode() decodes to 0)
// must not land as 0000 (which OpenFile would otherwise create on Unix and
// which would make the file unreadable after close) — it defaults to 0644.
// Windows has no usable unix mode reporting, so the assertion is skipped.
func TestExtractZipDefaultsTo0644WhenModeAbsent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not track unix exec permission bits")
	}
	zipPath := filepath.Join(t.TempDir(), "nomode.zip")
	if err := writeTestZipWithModes(zipPath, map[string]zipFixture{
		"data.bin": {Content: "payload", Mode: 0000},
	}); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("extractZip failed: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dest, "data.bin"))
	if err != nil {
		t.Fatalf("extracted file missing: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0644 {
		t.Errorf("mode-less zip entry extracted with mode %v, want 0644 default", got)
	}
}

// TestExtractZipEntryCountCap verifies extractZip aborts archives carrying
// more entries than maxExtractZipEntries: the per-file and total size caps do
// not bound entry count, so an entry-count bomb needs its own ceiling. The
// check runs up front (zip.OpenReader has already read the central directory),
// so nothing is extracted when the cap aborts.
func TestExtractZipEntryCountCap(t *testing.T) {
	setLanguageForTest(t, "zh")
	orig := maxExtractZipEntries
	maxExtractZipEntries = 2
	defer func() { maxExtractZipEntries = orig }()

	zipPath := filepath.Join(t.TempDir(), "many.zip")
	if err := writeTestZip(zipPath, map[string]string{
		"a.txt": "A",
		"b.txt": "B",
		"c.txt": "C",
	}); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	err := extractZip(zipPath, dest)
	if err == nil {
		t.Fatal("超过条目数上限的 zip 应返回错误")
	}
	if !strings.Contains(err.Error(), "条目数超出上限") {
		t.Errorf("错误信息应说明条目数超限: %v", err)
	}
	entries, readErr := os.ReadDir(dest)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Errorf("上限应在解压前中止, 不应写出任何条目, 实际 %d 个", len(entries))
	}

	// Control: at exactly the cap, extraction succeeds.
	ctrl := filepath.Join(t.TempDir(), "ok.zip")
	if err := writeTestZip(ctrl, map[string]string{"a.txt": "A", "b.txt": "B"}); err != nil {
		t.Fatal(err)
	}
	dest2 := t.TempDir()
	if err := extractZip(ctrl, dest2); err != nil {
		t.Fatalf("恰好在条目数上限的 zip 不应报错: %v", err)
	}
}
