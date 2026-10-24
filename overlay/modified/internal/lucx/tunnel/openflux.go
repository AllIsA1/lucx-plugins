// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package tunnel

import (
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	openfluxDefaultPort = 18445
	openfluxContext     = "http://openflux"
	openfluxMinSecret   = 16
	openfluxLinkPrefix  = "openflux://v1/"
)

// OpenfluxConfig is one OpenFlux exit. Direct always listens on the inbound
// port. Document URLs are optional extra carriers; each field may name several
// links of its type (the core runs them as one striped group). The exit serves
// one active client; the link is the credential. ExitMode is l4 (gVisor, no
// root needed, the default) or l3 (raw forward, faster, needs the RST drop rule).
type OpenfluxConfig struct {
	Secret      string `json:"secret"`
	ShareHost   string `json:"shareHost"`
	YandexURL   string `json:"yandexUrl"`
	MailruURL   string `json:"mailruUrl"`
	CupsURL     string `json:"cupsUrl"`
	VolgaURL    string `json:"volgaUrl"`
	BoardURL    string `json:"boardUrl"`
	AvitoURL    string `json:"avitoUrl"`
	TelemostURL string `json:"telemostUrl"`
	ExitMode    string `json:"exitMode"`
}

const (
	OpenfluxModeL4 = "l4"
	OpenfluxModeL3 = "l3"

	// openfluxMaxLinks bounds the links in one field: the core's stripe has a lane limit anyway.
	openfluxMaxLinks = 8
)

// openfluxCarrier is a document carrier a field of the config feeds: the transport type the core knows it by,
// its failover priority (higher is tried first) and the field it reads.
type openfluxCarrier struct {
	typ      string
	priority int
	url      func(OpenfluxConfig) string
}

// openfluxCarriers lists them in the order of the fields in the form. The first non-empty one is also the
// session's encryption context, as before.
var openfluxCarriers = []openfluxCarrier{
	{"yandex", 50, func(c OpenfluxConfig) string { return c.YandexURL }},
	{"mailru", 40, func(c OpenfluxConfig) string { return c.MailruURL }},
	{"cupsonline", 30, func(c OpenfluxConfig) string { return c.CupsURL }},
	{"vyandex", 48, func(c OpenfluxConfig) string { return c.VolgaURL }},
	{"boards", 46, func(c OpenfluxConfig) string { return c.BoardURL }},
	{"telemost", 44, func(c OpenfluxConfig) string { return c.TelemostURL }},
	{"avito", 35, func(c OpenfluxConfig) string { return c.AvitoURL }},
}

func DefaultOpenfluxConfig() OpenfluxConfig {
	return OpenfluxConfig{}
}

func (c OpenfluxConfig) Merge() OpenfluxConfig {
	c.Secret = strings.TrimSpace(c.Secret)
	c.ShareHost = strings.TrimSpace(c.ShareHost)
	c.YandexURL = strings.TrimSpace(c.YandexURL)
	c.MailruURL = strings.TrimSpace(c.MailruURL)
	c.CupsURL = strings.TrimSpace(c.CupsURL)
	c.VolgaURL = openfluxLinks(c.VolgaURL)
	c.BoardURL = openfluxLinks(c.BoardURL)
	c.AvitoURL = openfluxLinks(c.AvitoURL)
	c.TelemostURL = openfluxLinks(c.TelemostURL)
	c.YandexURL = openfluxLinks(c.YandexURL)
	c.MailruURL = openfluxLinks(c.MailruURL)
	c.CupsURL = openfluxLinks(c.CupsURL)
	c.ExitMode = strings.ToLower(strings.TrimSpace(c.ExitMode))
	if c.ExitMode == "" {
		c.ExitMode = OpenfluxModeL4
	}
	return c
}

// openfluxLinks puts the links of one field in one form: separated by a comma, as the core reads them.
// People paste them one per line, with spaces or with '|'.
func openfluxLinks(raw string) string {
	return strings.Join(openfluxSplitLinks(raw), ",")
}

func openfluxSplitLinks(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '|' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
}

func (c OpenfluxConfig) EnsureSecret() (OpenfluxConfig, error) {
	if c.Secret != "" {
		return c, nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return c, err
	}
	c.Secret = hex.EncodeToString(buf)
	return c, nil
}

func (c OpenfluxConfig) Validate(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("openflux: port must be 1-65535")
	}
	if openfluxSecretChars(c.Secret) < openfluxMinSecret {
		return fmt.Errorf("openflux: secret must be at least %d characters", openfluxMinSecret)
	}
	if err := openfluxHostOK(c.ShareHost); err != nil {
		return err
	}
	if m := strings.ToLower(strings.TrimSpace(c.ExitMode)); m != "" && m != OpenfluxModeL4 && m != OpenfluxModeL3 {
		return errors.New("openflux: exit mode must be l4 or l3")
	}
	for _, car := range openfluxCarriers {
		links := openfluxSplitLinks(car.url(c))
		if len(links) > openfluxMaxLinks {
			return fmt.Errorf("openflux: at most %d links per carrier", openfluxMaxLinks)
		}
		for _, raw := range links {
			if err := openfluxDocURL(raw); err != nil {
				return err
			}
		}
	}
	return nil
}

// exitMode is the mode the exit runs in; anything but l3 is l4.
func (c OpenfluxConfig) exitMode() string {
	if strings.EqualFold(strings.TrimSpace(c.ExitMode), OpenfluxModeL3) {
		return OpenfluxModeL3
	}
	return OpenfluxModeL4
}

// UsesL3 says whether the exit forwards raw packets, which needs the host's RST drop rule.
func (c OpenfluxConfig) UsesL3() bool { return c.exitMode() == OpenfluxModeL3 }

func (c OpenfluxConfig) contextURL() string {
	for _, car := range openfluxCarriers {
		if u := car.url(c); u != "" {
			return u
		}
	}
	return openfluxContext
}

func (c OpenfluxConfig) listenAddr(port int) string {
	return net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
}

func (c OpenfluxConfig) dialAddr(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		host = c.ShareHost
	}
	if host == "" || port < 1 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// RenderConf is the exit .conf. Mode is l4 unless the inbound asks for l3
// (the panel then installs the kernel RST drop rule). A [Transport] section
// makes the binary run a negotiated session.
func (c OpenfluxConfig) RenderConf(key string, port int) string {
	secretPath := absPath(filepath.Join(workDir(), key+"-secret.txt"))
	cookiePath := absPath(filepath.Join(dataDirFor(key, Openflux), "cookies.json"))
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nRole = exit\nMode = %s\nCodec = batched\nEncryptionKeyFile = %s\nURL = %s\nCookieStore = %s\n",
		c.exitMode(), secretPath, c.contextURL(), cookiePath)
	fmt.Fprintf(&b, "\n[Transport \"direct\"]\nType = direct\nPriority = 100\nListen = %s\n", c.listenAddr(port))
	for _, car := range openfluxCarriers {
		if raw := car.url(c); raw != "" {
			fmt.Fprintf(&b, "\n[Transport \"%s\"]\nType = %s\nPriority = %d\nURL = %s\n", car.typ, car.typ, car.priority, raw)
		}
	}
	return b.String()
}

func (c OpenfluxConfig) secretFileName(key string) string {
	return key + "-secret.txt"
}

// ClientURI is openflux://v1/ + raw DEFLATE + base64url. Empty when the
// public host is unknown (direct needs a dial address).
func (c OpenfluxConfig) ClientURI(name, host string, port int) (string, error) {
	dial := c.dialAddr(host, port)
	if dial == "" || c.Secret == "" {
		return "", nil
	}
	payload := openfluxShare{
		Name:       strings.TrimSpace(name),
		Negotiate:  true,
		Secret:     c.Secret,
		Context:    c.contextURL(),
		Transports: c.shareTransports(dial),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(raw); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return openfluxLinkPrefix + base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

func (c OpenfluxConfig) shareTransports(dial string) []openfluxShareTransport {
	out := []openfluxShareTransport{{
		Type:     "direct",
		Priority: 100,
		Dial:     dial,
	}}
	for _, car := range openfluxCarriers {
		if raw := car.url(c); raw != "" {
			out = append(out, openfluxShareTransport{Type: car.typ, Priority: car.priority, URL: raw})
		}
	}
	return out
}

type openfluxShare struct {
	Name       string                   `json:"name,omitempty"`
	Negotiate  bool                     `json:"negotiate,omitempty"`
	Secret     string                   `json:"secret,omitempty"`
	Context    string                   `json:"context,omitempty"`
	Transports []openfluxShareTransport `json:"transports"`
}

type openfluxShareTransport struct {
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	Priority int    `json:"priority,omitempty"`
	Dial     string `json:"dial,omitempty"`
}

func openfluxSecretChars(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func openfluxHostOK(host string) error {
	if host == "" {
		return nil
	}
	if strings.ContainsAny(host, " /#;?") || strings.Contains(host, "://") {
		return errors.New("openflux: shareHost must be a host or IP, not a URL")
	}
	return nil
}

func openfluxDocURL(raw string) error {
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, "#;\r\n") {
		return errors.New("openflux: document URL cannot contain # or ;")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("openflux: document URL must be http(s)")
	}
	return nil
}
