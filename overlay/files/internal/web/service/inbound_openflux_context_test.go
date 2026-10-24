// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package service

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// Editing the first carrier link of an OpenFlux inbound must not change its encryption context, or every
// client link already handed out stops working.
func TestOpenfluxContextStaysPinnedAcrossSaves(t *testing.T) {
	s := &InboundService{}
	first := &model.Inbound{Protocol: model.Openflux, Port: 18445, Settings: `{"secret":"0123456789abcdef0123","yandexUrl":"https://docs.yandex.ru/edit/d/AAA"}`}
	if err := s.normalizeOpenfluxSettings(first, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.Settings, `"context": "https://docs.yandex.ru/edit/d/AAA"`) {
		t.Fatalf("the context was not pinned on the first save: %s", first.Settings)
	}
	edited := strings.Replace(first.Settings, `"yandexUrl": "https://docs.yandex.ru/edit/d/AAA"`, `"yandexUrl": "https://docs.yandex.ru/edit/d/BBB"`, 1)
	second := &model.Inbound{Protocol: model.Openflux, Port: 18445, Settings: edited}
	if err := s.normalizeOpenfluxSettings(second, first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.Settings, `"context": "https://docs.yandex.ru/edit/d/AAA"`) || !strings.Contains(second.Settings, "d/BBB") {
		t.Fatalf("the context moved with the link, or the link was lost: %s", second.Settings)
	}
}
