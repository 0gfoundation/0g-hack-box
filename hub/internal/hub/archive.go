package hub

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// safeName returns the cleaned relative name of a tar entry, or "" when the
// entry must be skipped: absolute paths, any ".." component, empty names.
//
// The box archives ~/project relative to the home directory, so entries look
// like "project/src/main.py". A leading "project/" is dropped; the caller puts
// everything under its own top folder.
func safeName(name string) string {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) {
		return ""
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return ""
		}
	}
	clean := path.Clean(name)
	clean = strings.TrimPrefix(clean, "./")
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return ""
	}
	if clean == "project" {
		return ""
	}
	clean = strings.TrimPrefix(clean, "project/")
	return clean
}

// walkTarGz calls fn for every regular file and directory with a safe name.
// Symlinks, hard links, devices, fifos and unsafe names are skipped.
func walkTarGz(r io.Reader, fn func(name string, hdr *tar.Header, body io.Reader) error) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA, tar.TypeDir: //nolint:staticcheck // TypeRegA from old tars
		default:
			continue
		}
		name := safeName(hdr.Name)
		if name == "" {
			continue
		}
		if err := fn(name, hdr, tr); err != nil {
			return err
		}
	}
}

// tarGzToZip converts a tar.gz stream into a zip written to w, with every
// entry under top (for example "K7MZQ2-project/").
func tarGzToZip(r io.Reader, w io.Writer, top string) error {
	zw := zip.NewWriter(w)
	top = strings.TrimSuffix(top, "/") + "/"
	if _, err := zw.CreateHeader(&zip.FileHeader{Name: top, Method: zip.Store, Modified: time.Now()}); err != nil {
		return err
	}
	seen := map[string]bool{top: true}
	err := walkTarGz(r, func(name string, hdr *tar.Header, body io.Reader) error {
		fh := &zip.FileHeader{Name: top + name, Modified: hdr.ModTime}
		if hdr.Typeflag == tar.TypeDir {
			fh.Name += "/"
			fh.Method = zip.Store
			fh.SetMode(os.ModeDir | (os.FileMode(hdr.Mode) & 0o755) | 0o700)
		} else {
			fh.Method = zip.Deflate
			fh.SetMode((os.FileMode(hdr.Mode) & 0o755) | 0o600)
		}
		if seen[fh.Name] {
			return nil
		}
		seen[fh.Name] = true
		fw, err := zw.CreateHeader(fh)
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeDir {
			_, err = io.Copy(fw, body)
		}
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// hasGitDir reports whether any path component is a .git directory. The
// attendee's own git metadata must never reach the hub's git: a crafted
// .git/config (core.fsmonitor, hooks) would run commands on the hub.
func hasGitDir(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

// unpackTarGz extracts the safe entries of a tar.gz into dir, leaving out
// any .git directory.
func unpackTarGz(r io.Reader, dir string) error {
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	return walkTarGz(r, func(name string, hdr *tar.Header, body io.Reader) error {
		if hasGitDir(name) {
			return nil
		}
		dst := filepath.Join(root, filepath.FromSlash(name))
		if !strings.HasPrefix(dst, root+string(filepath.Separator)) {
			return nil // belt and braces; safeName already refused this
		}
		if hdr.Typeflag == tar.TypeDir {
			return os.MkdirAll(dst, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		// Refuse to write through anything that is not a plain directory or file.
		if fi, err := os.Lstat(dst); err == nil && !fi.Mode().IsRegular() {
			return fmt.Errorf("refusing to overwrite %s", name)
		}
		mode := os.FileMode(0o644)
		if hdr.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, body)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	})
}
