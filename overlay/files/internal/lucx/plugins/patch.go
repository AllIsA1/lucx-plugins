// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// MaxPatchOps bounds how many operations one plugin may return per pipeline run.
	MaxPatchOps = 200
	// MaxPatchedBytes bounds the document size after patching.
	MaxPatchedBytes = 4 << 20
)

// PatchOp is one RFC 6902 operation. Only add, remove, replace and test are
// accepted: they are enough to edit a config and every change stays visible.
type PatchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

// ApplyPatch applies ops to a JSON document atomically: any failing
// operation fails the whole patch and nothing is returned. Numbers keep every digit.
func ApplyPatch(doc []byte, ops []PatchOp) ([]byte, error) {
	if len(ops) > MaxPatchOps {
		return nil, fmt.Errorf("patch has %d operations, the limit is %d", len(ops), MaxPatchOps)
	}
	root, err := decodeJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("document is not valid JSON: %w", err)
	}
	for i, op := range ops {
		root, err = applyOp(root, op)
		if err != nil {
			return nil, fmt.Errorf("operation %d (%s %s): %w", i, op.Op, op.Path, err)
		}
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxPatchedBytes {
		return nil, fmt.Errorf("patched document is too large (%d bytes, limit %d)", len(out), MaxPatchedBytes)
	}
	return out, nil
}

func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

func applyOp(root any, op PatchOp) (any, error) {
	switch op.Op {
	case "add", "replace", "test":
		if len(op.Value) == 0 {
			return nil, errors.New("a value is required")
		}
	case "remove":
	default:
		return nil, fmt.Errorf("unsupported operation %q", op.Op)
	}
	tokens, err := parsePointer(op.Path)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, errors.New("the document root cannot be modified")
	}
	var value any
	if len(op.Value) > 0 {
		if value, err = decodeJSON(op.Value); err != nil {
			return nil, fmt.Errorf("invalid value: %w", err)
		}
	}
	parent, err := walk(root, tokens[:len(tokens)-1])
	if err != nil {
		return nil, err
	}
	last := tokens[len(tokens)-1]
	switch p := parent.(type) {
	case map[string]any:
		return root, applyToObject(p, op.Op, last, value)
	case []any:
		return applyToArray(root, tokens[:len(tokens)-1], p, op.Op, last, value)
	default:
		return nil, errors.New("the parent is not a container")
	}
}

func applyToObject(obj map[string]any, op, key string, value any) error {
	_, exists := obj[key]
	switch op {
	case "add":
		obj[key] = value
	case "replace":
		if !exists {
			return fmt.Errorf("member %q does not exist", key)
		}
		obj[key] = value
	case "remove":
		if !exists {
			return fmt.Errorf("member %q does not exist", key)
		}
		delete(obj, key)
	case "test":
		if !exists || !jsonEqual(obj[key], value) {
			return errors.New("test failed")
		}
	}
	return nil
}

func applyToArray(root any, parentPath []string, arr []any, op, token string, value any) (any, error) {
	if op == "add" && token == "-" {
		return replaceAt(root, parentPath, append(arr, value))
	}
	idx, err := parseIndex(token)
	if err != nil {
		return nil, err
	}
	switch op {
	case "add":
		if idx > len(arr) {
			return nil, fmt.Errorf("index %d is out of range (length %d)", idx, len(arr))
		}
		out := append(arr[:idx:idx], append([]any{value}, arr[idx:]...)...)
		return replaceAt(root, parentPath, out)
	case "replace", "remove", "test":
		if idx >= len(arr) {
			return nil, fmt.Errorf("index %d does not exist (length %d)", idx, len(arr))
		}
		switch op {
		case "replace":
			arr[idx] = value
		case "remove":
			return replaceAt(root, parentPath, append(arr[:idx:idx], arr[idx+1:]...))
		case "test":
			if !jsonEqual(arr[idx], value) {
				return nil, errors.New("test failed")
			}
		}
	}
	return root, nil
}

// replaceAt swaps the container at path for next; arrays are values, so a
// length change must be written back into the parent.
func replaceAt(root any, path []string, next any) (any, error) {
	if len(path) == 0 {
		return next, nil
	}
	parent, err := walk(root, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	key := path[len(path)-1]
	switch p := parent.(type) {
	case map[string]any:
		p[key] = next
	case []any:
		idx, err := parseIndex(key)
		if err != nil || idx >= len(p) {
			return nil, errors.New("path does not exist")
		}
		p[idx] = next
	default:
		return nil, errors.New("the parent is not a container")
	}
	return root, nil
}

func walk(node any, tokens []string) (any, error) {
	for _, tok := range tokens {
		switch n := node.(type) {
		case map[string]any:
			child, ok := n[tok]
			if !ok {
				return nil, fmt.Errorf("member %q does not exist", tok)
			}
			node = child
		case []any:
			idx, err := parseIndex(tok)
			if err != nil {
				return nil, err
			}
			if idx >= len(n) {
				return nil, fmt.Errorf("index %d does not exist (length %d)", idx, len(n))
			}
			node = n[idx]
		default:
			return nil, errors.New("the path goes through a value that is not a container")
		}
	}
	return node, nil
}

func parsePointer(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("invalid pointer %q: it must start with /", p)
	}
	parts := strings.Split(p[1:], "/")
	for i, part := range parts {
		part = strings.ReplaceAll(part, "~1", "/")
		parts[i] = strings.ReplaceAll(part, "~0", "~")
	}
	return parts, nil
}

func parseIndex(tok string) (int, error) {
	if tok == "" || (len(tok) > 1 && tok[0] == '0') {
		return 0, fmt.Errorf("invalid array index %q", tok)
	}
	n, err := strconv.Atoi(tok)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid array index %q", tok)
	}
	return n, nil
}

func jsonEqual(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}
