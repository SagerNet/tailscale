// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Package precompress provides build- and serving-time support for
// precompressed static resources, to avoid the cost of repeatedly compressing
// unchanging resources.
package precompress

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/andybalholm/brotli"
	"github.com/sagernet/tailscale/tsweb"
	"golang.org/x/sync/errgroup"
)

// PrecompressDir compresses static assets in dirPath using Gzip and Brotli, so
// that they can be later served with OpenPrecompressedFile.
func PrecompressDir(dirPath string, options Options) error {
	var eg errgroup.Group
	err := fs.WalkDir(os.DirFS(dirPath), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !compressibleExtensions[filepath.Ext(p)] {
			return nil
		}
		p = path.Join(dirPath, p)
		if options.ProgressFn != nil {
			options.ProgressFn(p)
		}

		eg.Go(func() error {
			return Precompress(p, options)
		})
		return nil
	})
	if err != nil {
		return err
	}
	return eg.Wait()
}

type Options struct {
	// FastCompression controls whether compression should be optimized for
	// speed rather than size.
	FastCompression bool
	// ProgressFn, if non-nil, is invoked when a file in the directory is about
	// to be compressed.
	ProgressFn func(path string)
}

// OpenPrecompressedFile opens a file from fs, preferring compressed versions
// generated by PrecompressDir if possible.
func OpenPrecompressedFile(w http.ResponseWriter, r *http.Request, path string, fs fs.FS) (fs.File, error) {
	if tsweb.AcceptsEncoding(r, "br") {
		if f, err := fs.Open(path + ".br"); err == nil {
			w.Header().Set("Content-Encoding", "br")
			return f, nil
		}
	}
	if tsweb.AcceptsEncoding(r, "gzip") {
		if f, err := fs.Open(path + ".gz"); err == nil {
			w.Header().Set("Content-Encoding", "gzip")
			return f, nil
		}
	}

	return fs.Open(path)
}

var compressibleExtensions = map[string]bool{
	".js":  true,
	".css": true,
}

func Precompress(path string, options Options) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}

	gzipLevel := gzip.BestCompression
	if options.FastCompression {
		gzipLevel = gzip.BestSpeed
	}
	err = writeCompressed(contents, func(w io.Writer) (io.WriteCloser, error) {
		return gzip.NewWriterLevel(w, gzipLevel)
	}, path+".gz", fi.Mode())
	if err != nil {
		return err
	}
	brotliLevel := brotli.BestCompression
	if options.FastCompression {
		brotliLevel = brotli.BestSpeed
	}
	return writeCompressed(contents, func(w io.Writer) (io.WriteCloser, error) {
		return brotli.NewWriterLevel(w, brotliLevel), nil
	}, path+".br", fi.Mode())
}

func writeCompressed(contents []byte, compressedWriterCreator func(io.Writer) (io.WriteCloser, error), outputPath string, outputMode fs.FileMode) error {
	var buf bytes.Buffer
	compressedWriter, err := compressedWriterCreator(&buf)
	if err != nil {
		return err
	}
	if _, err := compressedWriter.Write(contents); err != nil {
		return err
	}
	if err := compressedWriter.Close(); err != nil {
		return err
	}
	return os.WriteFile(outputPath, buf.Bytes(), outputMode)
}
