// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"encoding/json"
	"strings"
	"testing"
)

func ops(t *testing.T, raw string) []PatchOp {
	t.Helper()
	var out []PatchOp
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("bad test patch %s: %v", raw, err)
	}
	return out
}

func canon(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("result is not JSON: %v: %s", err, raw)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func TestApplyPatch(t *testing.T) {
	cases := []struct {
		name  string
		doc   string
		patch string
		want  string
	}{
		{"add member", `{"a":1}`, `[{"op":"add","path":"/b","value":2}]`, `{"a":1,"b":2}`},
		{"add replaces an existing member", `{"a":1}`, `[{"op":"add","path":"/a","value":9}]`, `{"a":9}`},
		{"add into a nested object", `{"routing":{"rules":[]}}`, `[{"op":"add","path":"/routing/domainStrategy","value":"AsIs"}]`, `{"routing":{"domainStrategy":"AsIs","rules":[]}}`},
		{"append to an array", `{"r":[1,2]}`, `[{"op":"add","path":"/r/-","value":3}]`, `{"r":[1,2,3]}`},
		{"insert into an array", `{"r":[1,3]}`, `[{"op":"add","path":"/r/1","value":2}]`, `{"r":[1,2,3]}`},
		{"insert at the end by index", `{"r":[1]}`, `[{"op":"add","path":"/r/1","value":2}]`, `{"r":[1,2]}`},
		{"replace", `{"a":1}`, `[{"op":"replace","path":"/a","value":{"x":true}}]`, `{"a":{"x":true}}`},
		{"replace an array element", `{"r":[1,2,3]}`, `[{"op":"replace","path":"/r/1","value":20}]`, `{"r":[1,20,3]}`},
		{"remove member", `{"a":1,"b":2}`, `[{"op":"remove","path":"/a"}]`, `{"b":2}`},
		{"remove array element", `{"r":[1,2,3]}`, `[{"op":"remove","path":"/r/0"}]`, `{"r":[2,3]}`},
		{"test then add", `{"a":1}`, `[{"op":"test","path":"/a","value":1},{"op":"add","path":"/b","value":2}]`, `{"a":1,"b":2}`},
		{"test compares objects structurally", `{"a":{"x":1,"y":2}}`, `[{"op":"test","path":"/a","value":{"y":2,"x":1}}]`, `{"a":{"x":1,"y":2}}`},
		{"pointer escapes", `{"a/b":1,"c~d":2}`, `[{"op":"replace","path":"/a~1b","value":10},{"op":"replace","path":"/c~0d","value":20}]`, `{"a/b":10,"c~d":20}`},
		{"big integers keep every digit", `{"id":12345678901234567890}`, `[{"op":"add","path":"/n","value":98765432109876543210}]`, `{"id":12345678901234567890,"n":98765432109876543210}`},
		{"empty patch", `{"a":1}`, `[]`, `{"a":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyPatch([]byte(tc.doc), ops(t, tc.patch))
			if err != nil {
				t.Fatalf("ApplyPatch: %v", err)
			}
			if canon(t, got) != canon(t, []byte(tc.want)) {
				t.Errorf("result = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestApplyPatchRejects(t *testing.T) {
	cases := []struct {
		name, doc, patch, want string
	}{
		{"remove a missing member", `{"a":1}`, `[{"op":"remove","path":"/b"}]`, "does not exist"},
		{"replace a missing member", `{"a":1}`, `[{"op":"replace","path":"/b","value":1}]`, "does not exist"},
		{"add under a missing parent", `{"a":1}`, `[{"op":"add","path":"/x/y","value":1}]`, "does not exist"},
		{"array index past the end", `{"r":[1]}`, `[{"op":"add","path":"/r/5","value":1}]`, "out of range"},
		{"negative or non-numeric index", `{"r":[1]}`, `[{"op":"add","path":"/r/x","value":1}]`, "index"},
		{"leading zero index", `{"r":[1,2]}`, `[{"op":"replace","path":"/r/01","value":1}]`, "index"},
		{"failed test", `{"a":1}`, `[{"op":"test","path":"/a","value":2}]`, "test failed"},
		{"move is not supported", `{"a":1}`, `[{"op":"move","path":"/b","from":"/a"}]`, "unsupported"},
		{"copy is not supported", `{"a":1}`, `[{"op":"copy","path":"/b","from":"/a"}]`, "unsupported"},
		{"unknown op", `{"a":1}`, `[{"op":"frobnicate","path":"/a"}]`, "unsupported"},
		{"root replace", `{"a":1}`, `[{"op":"replace","path":"","value":{}}]`, "root"},
		{"pointer without a leading slash", `{"a":1}`, `[{"op":"add","path":"a","value":1}]`, "pointer"},
		{"add without a value", `{"a":1}`, `[{"op":"add","path":"/b"}]`, "value"},
		{"descending into a scalar", `{"a":1}`, `[{"op":"add","path":"/a/b","value":1}]`, "not a container"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ApplyPatch([]byte(tc.doc), ops(t, tc.patch))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestApplyPatchIsAtomicAndBounded(t *testing.T) {
	doc := `{"a":1}`
	if _, err := ApplyPatch([]byte(doc), ops(t, `[{"op":"add","path":"/b","value":2},{"op":"remove","path":"/nope"}]`)); err == nil {
		t.Fatal("a patch with a failing operation must fail as a whole")
	}
	if _, err := ApplyPatch([]byte("{not json"), nil); err == nil {
		t.Error("an invalid document must be rejected")
	}
	many := make([]PatchOp, MaxPatchOps+1)
	for i := range many {
		many[i] = PatchOp{Op: "add", Path: "/x", Value: json.RawMessage(`1`)}
	}
	if _, err := ApplyPatch([]byte(doc), many); err == nil || !strings.Contains(err.Error(), "operations") {
		t.Errorf("too many operations: %v", err)
	}
	huge := PatchOp{Op: "add", Path: "/big", Value: json.RawMessage(`"` + strings.Repeat("a", MaxPatchedBytes) + `"`)}
	if _, err := ApplyPatch([]byte(doc), []PatchOp{huge}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("an oversized result: %v", err)
	}
}
