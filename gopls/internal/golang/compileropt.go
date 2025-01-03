// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

// CompilerOptDetails invokes the Go compiler with the "-json=0,dir"
// flag on the specified package, parses its log of optimization
// decisions, and returns them as a set of diagnostics.
func CompilerOptDetails(ctx context.Context, snapshot *cache.Snapshot, mp *metadata.Package) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	if len(mp.CompiledGoFiles) == 0 {
		return nil, nil
	}
	pkgDir := mp.CompiledGoFiles[0].DirPath()
	outDir, err := os.MkdirTemp("", fmt.Sprintf("gopls-%d.details", os.Getpid()))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := os.RemoveAll(outDir); err != nil {
			event.Error(ctx, "cleaning details dir", err)
		}
	}()

	tmpFile, err := os.CreateTemp(os.TempDir(), "gopls-x")
	if err != nil {
		return nil, err
	}
	tmpFile.Close() // ignore error
	defer os.Remove(tmpFile.Name())

	outDirURI := protocol.URIFromPath(outDir)
	// details doesn't handle Windows URIs in the form of "file:///C:/...",
	// so rewrite them to "file://C:/...". See golang/go#41614.
	if !strings.HasPrefix(outDir, "/") {
		outDirURI = protocol.DocumentURI(strings.Replace(string(outDirURI), "file:///", "file://", 1))
	}
	inv, cleanupInvocation, err := snapshot.GoCommandInvocation(cache.NoNetwork, pkgDir, "build", []string{
		fmt.Sprintf("-gcflags=-json=0,%s", outDirURI), // JSON schema version 0
		fmt.Sprintf("-o=%s", tmpFile.Name()),
		".",
	})
	if err != nil {
		return nil, err
	}
	defer cleanupInvocation()
	_, err = snapshot.View().GoCommandRunner().Run(ctx, *inv)
	if err != nil {
		return nil, err
	}
	files, err := findJSONFiles(outDir)
	if err != nil {
		return nil, err
	}
	reports := make(map[protocol.DocumentURI][]*cache.Diagnostic)
	var parseError error
	for _, fn := range files {
		uri, diagnostics, err := parseDetailsFile(fn)
		if err != nil {
			// expect errors for all the files, save 1
			parseError = err
		}
		fh := snapshot.FindFile(uri)
		if fh == nil {
			continue
		}
		if pkgDir != fh.URI().DirPath() {
			// https://github.com/golang/go/issues/42198
			// sometimes the detail diagnostics generated for files
			// outside the package can never be taken back.
			continue
		}
		reports[fh.URI()] = diagnostics
	}
	return reports, parseError
}

// parseDetailsFile parses the file written by the Go compiler which contains a JSON-encoded protocol.Diagnostic.
func parseDetailsFile(filename string) (protocol.DocumentURI, []*cache.Diagnostic, error) {
	buf, err := os.ReadFile(filename)
	if err != nil {
		return "", nil, err
	}
	var (
		uri         protocol.DocumentURI
		i           int
		diagnostics []*cache.Diagnostic
	)
	type metadata struct {
		File string `json:"file,omitempty"`
	}
	for dec := json.NewDecoder(bytes.NewReader(buf)); dec.More(); {
		// The first element always contains metadata.
		if i == 0 {
			i++
			m := new(metadata)
			if err := dec.Decode(m); err != nil {
				return "", nil, err
			}
			if !strings.HasSuffix(m.File, ".go") {
				continue // <autogenerated>
			}
			uri = protocol.URIFromPath(m.File)
			continue
		}
		d := new(protocol.Diagnostic)
		if err := dec.Decode(d); err != nil {
			return "", nil, err
		}
		if d.Source != "go compiler" {
			continue
		}
		d.Tags = []protocol.DiagnosticTag{} // must be an actual slice
		msg := d.Code.(string)
		if msg != "" {
			// Typical message prefixes gathered by grepping the source of
			// cmd/compile for literal arguments in calls to logopt.LogOpt.
			// (It is not a well defined set.)
			//
			// - canInlineFunction
			// - cannotInlineCall
			// - cannotInlineFunction
			// - copy
			// - escape
			// - escapes
			// - isInBounds
			// - isSliceInBounds
			// - iteration-variable-to-{heap,stack}
			// - leak
			// - loop-modified-{range,for}
			// - nilcheck
			msg = fmt.Sprintf("%s(%s)", msg, d.Message)
		}

		// zeroIndexedRange subtracts 1 from the line and
		// range, because the compiler output neglects to
		// convert from 1-based UTF-8 coordinates to 0-based UTF-16.
		// (See GOROOT/src/cmd/compile/internal/logopt/log_opts.go.)
		// TODO(rfindley): also translate UTF-8 to UTF-16.
		zeroIndexedRange := func(rng protocol.Range) protocol.Range {
			return protocol.Range{
				Start: protocol.Position{
					Line:      rng.Start.Line - 1,
					Character: rng.Start.Character - 1,
				},
				End: protocol.Position{
					Line:      rng.End.Line - 1,
					Character: rng.End.Character - 1,
				},
			}
		}

		var related []protocol.DiagnosticRelatedInformation
		for _, ri := range d.RelatedInformation {
			related = append(related, protocol.DiagnosticRelatedInformation{
				Location: protocol.Location{
					URI:   ri.Location.URI,
					Range: zeroIndexedRange(ri.Location.Range),
				},
				Message: ri.Message,
			})
		}
		diagnostic := &cache.Diagnostic{
			URI:      uri,
			Range:    zeroIndexedRange(d.Range),
			Message:  msg,
			Severity: d.Severity,
			Source:   cache.CompilerOptDetailsInfo, // d.Source is always "go compiler" as of 1.16, use our own
			Tags:     d.Tags,
			Related:  related,
		}
		diagnostics = append(diagnostics, diagnostic)
		i++
	}
	return uri, diagnostics, nil
}

func findJSONFiles(dir string) ([]string, error) {
	ans := []string{}
	f := func(path string, fi os.FileInfo, _ error) error {
		if fi.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".json") {
			ans = append(ans, path)
		}
		return nil
	}
	err := filepath.Walk(dir, f)
	return ans, err
}
