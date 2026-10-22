// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// validateXrayPatch protects what the panel itself depends on: the core API,
// statistics and metrics sections, and every inbound that already existed (a
// plugin may add to the config, not remove or rename what clients connect to).
func validateXrayPatch(before, after []byte) error {
	var b, a map[string]json.RawMessage
	if err := json.Unmarshal(before, &b); err != nil {
		return fmt.Errorf("original config is not an object: %w", err)
	}
	if err := json.Unmarshal(after, &a); err != nil {
		return fmt.Errorf("result is not a JSON object: %w", err)
	}
	for _, section := range []string{"api", "stats", "metrics"} {
		same, err := sameJSON(b[section], a[section])
		if err != nil || !same {
			return fmt.Errorf("the %s section must not be changed", section)
		}
	}
	beforeIn, err := inboundsByTag(b["inbounds"])
	if err != nil {
		return fmt.Errorf("original inbounds: %w", err)
	}
	afterIn, err := inboundsByTag(a["inbounds"])
	if err != nil {
		return fmt.Errorf("inbounds must stay an array of objects: %w", err)
	}
	apiTag := apiInboundTag(b["api"])
	for tag, raw := range beforeIn {
		now, ok := afterIn[tag]
		if !ok {
			return fmt.Errorf("inbound %q must not be removed or renamed", tag)
		}
		if tag == apiTag {
			if same, err := sameJSON(raw, now); err != nil || !same {
				return errors.New("the api inbound must not be changed")
			}
		}
	}
	return nil
}

func apiInboundTag(api json.RawMessage) string {
	var v struct {
		Tag string `json:"tag"`
	}
	_ = json.Unmarshal(api, &v)
	return v.Tag
}

func inboundsByTag(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(list))
	for _, item := range list {
		var head struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(item, &head); err != nil {
			return nil, err
		}
		if head.Tag != "" {
			out[head.Tag] = item
		}
	}
	return out, nil
}

func sameJSON(a, b json.RawMessage) (bool, error) {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b), nil
	}
	ca, err := canonicalJSON(a)
	if err != nil {
		return false, err
	}
	cb, err := canonicalJSON(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ca, cb), nil
}

func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
