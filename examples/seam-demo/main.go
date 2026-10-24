// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// A comment line most subscription clients skip. A real plugin would add something useful here.
const subscriptionLine = "# served with the Seam Demo plugin"

func main() {
	mux := http.NewServeMux()
	mux.Handle("POST /_lucx/seam/{mode}", SeamHandler(onSeam))
	log.Print("seam-demo plugin ready")
	if err := Serve(mux); err != nil {
		log.Fatal(err)
	}
}

// onSeam is called for every seam declared in lucx-plugin.json. Whatever it returns, the panel falls
// back to its own behaviour when this plugin is off, slow or wrong.
func onSeam(c SeamCall) (SeamReply, error) {
	switch {
	case c.Seam.Engine == "sub" && c.Seam.Mode == "after":
		return addSubscriptionLine(c), nil
	case c.Seam.Engine == "api" && c.Seam.Mode == "before":
		return teapot(c), nil
	case c.Seam.Engine == "api" && c.Seam.Mode == "after":
		return Modify(ResponseChange{Headers: map[string][]string{"X-Seam-Demo": {"1"}}}), nil
	}
	return SeamReply{}, fmt.Errorf("no seam %s/%s/%s is declared", c.Seam.Engine, c.Seam.Mode, c.Seam.Route)
}

// addSubscriptionLine appends a line to a subscription: base64 of one link per line, or the same links as
// plain text when the panel's "encrypt subscription" setting is off. Anything else, such as an error
// page, an HTML page or an answer that already has the line, stays as it is.
func addSubscriptionLine(c SeamCall) SeamReply {
	if c.Response == nil || c.Response.Status != http.StatusOK || len(c.Response.Body) == 0 {
		return Keep()
	}
	body := string(c.Response.Body)
	if strings.Contains(body, subscriptionLine) {
		return Keep()
	}
	if decoded, err := base64.StdEncoding.DecodeString(body); err == nil {
		changed := base64.StdEncoding.EncodeToString([]byte(strings.TrimRight(string(decoded), "\n") + "\n" + subscriptionLine))
		if strings.Contains(string(decoded), subscriptionLine) {
			return Keep()
		}
		return Modify(ResponseChange{Body: Bytes([]byte(changed))})
	}
	if isPlainLinks(c.Response) {
		return Modify(ResponseChange{Body: Bytes([]byte(strings.TrimRight(body, "\n") + "\n" + subscriptionLine))})
	}
	return Keep()
}

// isPlainLinks: a text/plain answer that holds links (vless://, vmess://, ...).
func isPlainLinks(r *SeamResponse) bool {
	for name, values := range r.Headers {
		if strings.EqualFold(name, "Content-Type") {
			for _, v := range values {
				if strings.HasPrefix(strings.ToLower(v), "text/plain") && strings.Contains(string(r.Body), "://") {
					return true
				}
			}
		}
	}
	return false
}

// teapot answers GET /server/status?teapot=1 itself and lets every other request go on.
func teapot(c SeamCall) SeamReply {
	if q, err := url.ParseQuery(c.Request.Query); err == nil && q.Get("teapot") == "1" {
		return Respond(http.StatusTeapot, map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}}, []byte("I am a teapot, said the plugin\n"))
	}
	return Continue(nil)
}
