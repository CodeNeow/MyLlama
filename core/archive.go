package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ─── Archive extraction ─────────────────────────────────────────
// Safe zip / tar.gz extraction with per-file and total size caps against
// extraction bombs.

// Extraction size caps (declared as vars so tests can shrink them for
// verification). Prevent zip/tar extraction bombs from filling the disk or
// exhausting memory (#2).
var maxExtractFileSize int64 = 4 << 30   // per-file extraction cap: 4GB
var maxExtractTotalSize int64 = 16 << 30 // per-run total extraction cap: 16GB

// maxExtractZipEntries caps how many entries extractZip will process. Real
// llama.cpp release zips carry a few dozen entries (binaries plus GGML backend
// libraries), so 20000 sits far above any legitimate archive while cutting zip
// bombs whose damage comes from millions of tiny entries — the per-file and
// total size caps do not bound entry count. Declared as a var (same style as
// the size caps) so tests can shrink it. Exceeding the cap aborts the
// extraction up front (zip.OpenReader has already read the central directory,
// so the check runs before anything is written), following the function's
// existing error semantics: nothing new has been extracted at that point.
var maxExtractZipEntries = 20000

func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if len(r.File) > maxExtractZipEntries {
		return fmt.Errorf(tr("zip 条目数超出上限: %d", "zip entry count exceeds the limit: %d"), len(r.File))
	}

	var totalBytes int64
	for _, f := range r.File {
		path := filepath.Join(dest, f.Name)

		// Prevent zip slip
		cleanPath := filepath.Clean(path)
		if !strings.HasPrefix(cleanPath, filepath.Clean(dest)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(path, 0755)
			continue
		}

		// Reject oversized files up front by declared size, instead of
		// writing them out first and discovering the limit afterwards
		if f.UncompressedSize64 > uint64(maxExtractFileSize) {
			return fmt.Errorf(tr("文件超出解压大小上限: %s", "file exceeds the extraction size limit: %s"), path)
		}

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		// Extraction permission: entries created on Unix carry their real mode
		// bits (including the exec bit for binaries) in the creator-unix upper
		// external-attribute word, which f.Mode() decodes; entries with no unix
		// mode at all (Windows-created zips decode via the DOS attributes,
		// broken unix encodings decode to 0) fall back to 0644 so a regular
		// data file never lands unusable.
		mode := f.Mode()
		if mode == 0 {
			mode = 0644
		}
		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return err
		}

		// io.CopyN copies at most maxExtractFileSize+1 bytes: a src exactly at
		// the cap returns (max, io.EOF), a src beyond the cap returns
		// (max+1, nil); hence the n > maxExtractFileSize over-limit check (#2).
		n, copyErr := io.CopyN(outFile, rc, maxExtractFileSize+1)
		rc.Close()
		outFile.Close()
		// Re-assert the entry mode after writing (best-effort): the creation
		// mode passed to OpenFile is filtered by the process umask, which can
		// strip the exec / group / other bits the archive intended. On Unix
		// this is what actually preserves the exec bit end-to-end.
		os.Chmod(path, mode)
		if copyErr != nil && copyErr != io.EOF {
			return copyErr
		}
		if n > maxExtractFileSize {
			return fmt.Errorf(tr("文件超出解压大小上限: %s", "file exceeds the extraction size limit: %s"), path)
		}
		totalBytes += n
		if totalBytes > maxExtractTotalSize {
			return fmt.Errorf(tr("解压总大小超出上限: %d 字节", "total extraction size exceeds the limit: %d bytes"), totalBytes)
		}
	}
	return nil
}

func extractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gzReader, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	var totalBytes int64
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		path := filepath.Join(dest, header.Name)

		// Prevent path traversal
		cleanPath := filepath.Clean(path)
		if !strings.HasPrefix(cleanPath, filepath.Clean(dest)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(path, 0755)
		case tar.TypeReg:
			// Reject oversized files up front by declared entry size (#2)
			if header.Size > maxExtractFileSize {
				return fmt.Errorf(tr("文件超出解压大小上限: %s", "file exceeds the extraction size limit: %s"), path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			// Apply the tar header's permission bits (upstream llama.cpp
			// release tarballs mark bin/ entries 0755): header.FileInfo().Mode()
			// decodes them so an extracted llama-server stays executable on
			// Unix (linux / macOS / Android).
			mode := header.FileInfo().Mode()
			outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			n, copyErr := io.CopyN(outFile, tarReader, maxExtractFileSize+1)
			outFile.Close()
			// Re-assert the header mode after writing (best-effort): the
			// creation mode passed to OpenFile is filtered by the process
			// umask, which can strip the exec / group / other bits the
			// archive intended. On Unix this is what actually preserves the
			// exec bit end-to-end.
			os.Chmod(path, mode)
			if copyErr != nil && copyErr != io.EOF {
				return copyErr
			}
			if n > maxExtractFileSize {
				return fmt.Errorf(tr("文件超出解压大小上限: %s", "file exceeds the extraction size limit: %s"), path)
			}
			totalBytes += n
			if totalBytes > maxExtractTotalSize {
				return fmt.Errorf(tr("解压总大小超出上限: %d 字节", "total extraction size exceeds the limit: %d bytes"), totalBytes)
			}
		default:
			// Explicitly reject symlinks/hardlinks/device files and other
			// unknown types, avoiding silently skipped entries that would
			// leave an incomplete extraction or a potential security issue (#6)
			return fmt.Errorf(tr("不支持的 tar 条目类型 %d: %s", "unsupported tar entry type %d: %s"), header.Typeflag, header.Name)
		}
	}
	return nil
}
