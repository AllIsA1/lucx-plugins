// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"testing"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

type tarEntry struct {
	name string
	typ  byte
	body string
	mode int64
	link string
}

func file(name, body string) tarEntry {
	return tarEntry{name: name, typ: tar.TypeReg, body: body, mode: 0o644}
}

func dir(name string) tarEntry {
	return tarEntry{name: name, typ: tar.TypeDir, mode: 0o755}
}

func buildArchive(t *testing.T, commit string, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if commit != "" {
		hdr := &tar.Header{
			Typeflag:   tar.TypeXGlobalHeader,
			Name:       "pax_global_header",
			PAXRecords: map[string]string{"comment": commit},
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write global header: %v", err)
		}
	}
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link}
		if e.typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func manifestJSON(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	m := map[string]any{
		"id":          "traffic-alerts",
		"name":        "Traffic Alerts",
		"version":     "1.2.0",
		"apiVersion":  "1",
		"description": "Notifies about traffic limits",
		"author":      "someone",
		"license":     "MIT",
		"type":        "process",
		"run": map[string]any{
			"command": "bin/${arch}/alerts",
			"args":    []string{"--socket", "${socket}"},
		},
		"permissions": []string{"api:clients:read", "events:client.traffic.exhausted"},
	}
	if mutate != nil {
		mutate(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return b
}
