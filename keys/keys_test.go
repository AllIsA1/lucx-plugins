// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package keys

import (
	"bytes"
	"testing"
)

func TestEmbeddedKeysParseAndAreDistinct(t *testing.T) {
	list := Builds() // panics when the embedded file is malformed
	if len(list) != 2 {
		t.Fatalf("got %d keys, want the current key and a spare", len(list))
	}
	if bytes.Equal(list[0], list[1]) {
		t.Error("the spare key must differ from the current one")
	}
}
