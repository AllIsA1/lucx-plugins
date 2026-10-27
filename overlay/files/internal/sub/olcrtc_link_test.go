// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package sub

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// The subscription names an olcRTC link after the inbound as it is now, not after the name saved with its first
// settings, and leaves the name out when the inbound says so.
func TestGenOlcrtcLinkCarriesTheInboundName(t *testing.T) {
	key := strings.Repeat("ab", 32)
	ib := &model.Inbound{
		Protocol: model.Olcrtc,
		Enable:   true,
		Remark:   "Poland",
		Settings: `{"remark":"first name","provider":"vkcalls","roomId":"https://vk.ru/call/join/abc","cryptoKey":"` + key + `","transport":"vp8channel","vp8Fps":30,"vp8Batch":64}`,
	}
	s := &SubService{}
	want := "olcrtc://vkcalls?vp8channel<vp8-fps=30&vp8-batch=64>@https://vk.ru/call/join/abc#" + key
	if got := s.genOlcrtcLink(ib); got != want+"$Poland" {
		t.Errorf("link = %q, want %q", got, want+"$Poland")
	}
	ib.Settings = strings.Replace(ib.Settings, `"remark":"first name",`, `"remark":"first name","noLinkName":true,`, 1)
	if got := s.genOlcrtcLink(ib); got != want {
		t.Errorf("noLinkName: link = %q, want %q", got, want)
	}
	ib.Remark = ""
	ib.Settings = strings.Replace(ib.Settings, `"noLinkName":true,`, "", 1)
	if got := s.genOlcrtcLink(ib); got != want+"$first name" {
		t.Errorf("no inbound remark: link = %q", got)
	}
	ib.Enable = false
	if got := s.genOlcrtcLink(ib); got != "" {
		t.Errorf("a disabled inbound has no link, got %q", got)
	}
}
