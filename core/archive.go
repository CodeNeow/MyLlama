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

// maxExtractTarLinks caps how many symlink / hardlink entries extractTarGz
// materializes. Upstream tarballs carry the shared-library version-alias chain
// as a handful of links (libllama.so -> libllama.so.0 -> libllama.so.0.7.0 —
// 18 on macos-arm64, 10 on ubuntu-x64), so 64 sits above any legitimate
// archive while bounding the duplicated bytes a link-heavy bomb can create
// through the copy fallback (each copy is also charged to maxExtractTotalSize).
// Declared as a var (same style as the size caps) so tests can shrink it.
var maxExtractTarLinks = 64

// tarLink is a link entry deferred to the second extraction pass: the path to
// create, its resolved in-dest target, the archive's own (slash-separated,
// relative) link name, and whether the archive asked for a hardlink.
type tarLink struct {
	path     string
	target   string
	linkname string
	hard     bool
}

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
	var links []tarLink
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
		case tar.TypeSymlink, tar.TypeLink:
			// Library-version links are part of a valid llama.cpp install, not
			// a defect: the upstream macOS / Linux tarballs ship the alias
			// chain (libllama.so -> libllama.so.0 -> libllama.so.0.7.0) that the
			// dynamic loader resolves at load time, and the binaries reference
			// the aliased names. Aborting on the first such entry used to leave
			// the extraction half-finished with no llama-server at all (the
			// runtime then showed up as "not found" on every Unix platform).
			//
			// Links are collected and applied after the main loop because the
			// archive does not guarantee the target precedes the link. Only an
			// in-archive relative target that stays inside dest is accepted:
			// an absolute or escaping Linkname is a hard error (#6).
			if len(links) >= maxExtractTarLinks {
				return fmt.Errorf(tr("链接条目数超出上限: %d", "link entry count exceeds the limit: %d"), len(links))
			}
			l, err := resolveTarLink(dest, path, header)
			if err != nil {
				return err
			}
			links = append(links, l)
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
			// Explicitly reject device files, FIFOs, char devices and other
			// unknown types (symlinks / hardlinks are handled above), avoiding
			// silently skipped entries that would leave an incomplete extraction
			// or a potential security issue (#6)
			return fmt.Errorf(tr("不支持的 tar 条目类型 %d: %s", "unsupported tar entry type %d: %s"), header.Typeflag, header.Name)
		}
	}

	// Second pass: create the collected links now that every regular entry
	// exists. A copy is used when the platform refuses a real link (Windows
	// without the SeCreateObjectLinks privilege, filesystems without symlink
	// support); the copied file is byte-identical to the alias target, which is
	// all the dynamic loader needs.
	for _, l := range links {
		copied, err := applyTarLink(l)
		if err != nil {
			return err
		}
		totalBytes += copied
		if totalBytes > maxExtractTotalSize {
			return fmt.Errorf(tr("解压总大小超出上限: %d 字节", "total extraction size exceeds the limit: %d bytes"), totalBytes)
		}
	}
	return nil
}

// resolveTarLink turns a link header into a tarLink whose target is guaranteed
// to live inside dest. tar names (and their Linkname) are slash-separated, so
// both sides are converted with filepath.FromSlash before joining; the link is
// relative to its own directory, hence Dir(path) as the base. An empty,
// absolute, or dest-escaping Linkname is rejected: that is the symlink variant
// of the path-traversal defense every regular entry already gets (#6).
func resolveTarLink(dest, path string, header *tar.Header) (tarLink, error) {
	linkname := header.Linkname
	if linkname == "" || filepath.IsAbs(linkname) || strings.HasPrefix(linkname, "/") {
		return tarLink{}, fmt.Errorf(tr("链接目标超出解压目录: %s", "link target outside the extraction directory: %s"), header.Name)
	}
	target := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(linkname)))
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
		return tarLink{}, fmt.Errorf(tr("链接目标超出解压目录: %s", "link target outside the extraction directory: %s"), header.Name)
	}
	return tarLink{
		path:     path,
		target:   target,
		linkname: linkname,
		hard:     header.Typeflag == tar.TypeLink,
	}, nil
}

// applyTarLink creates one collected link and reports the bytes it duplicated
// (0 when a real link was created). The archive's own relative link name is
// used for the symlink so the install keeps upstream's relative layout and
// stays movable; hardlinks go straight to os.Link on the resolved target. When
// neither is available the target file is copied, and a link pointing at a
// directory becomes a directory. A stale symlink already sitting at path (from
// a previous partial extraction) is removed first so the copy fallback cannot
// write through it to some other location.
func applyTarLink(l tarLink) (int64, error) {
	// Link entries can name a path inside a directory the archive never declared
	// as a TypeDir entry (implicit directories); create the parent up front so a
	// valid install does not fail on the link pass.
	if err := os.MkdirAll(filepath.Dir(l.path), 0755); err != nil {
		return 0, err
	}
	if fi, err := os.Lstat(l.path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		os.Remove(l.path)
	}

	if l.hard {
		if err := os.Link(l.target, l.path); err == nil {
			return 0, nil
		}
	} else if err := os.Symlink(filepath.FromSlash(l.linkname), l.path); err == nil {
		return 0, nil
	}

	fi, err := os.Stat(l.target)
	if err != nil {
		return 0, fmt.Errorf(tr("链接目标不存在: %s", "link target is missing: %s"), l.target)
	}
	if fi.IsDir() {
		return 0, os.MkdirAll(l.path, 0755)
	}
	if err := copyFile(l.target, l.path); err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
