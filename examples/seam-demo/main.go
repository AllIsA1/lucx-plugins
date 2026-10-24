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

// addSubscriptionLine appends a line to a plain subscription (base64 of one link per line). Anything
// else, such as an error page, an HTML page or an answer that already has the line, stays as it is.
func addSubscriptionLine(c SeamCall) SeamReply {
	if c.Response == nil || c.Response.Status != http.StatusOK || len(c.Response.Body) == 0 {
		return Keep()
	}
	decoded, err := base64.StdEncoding.DecodeString(string(c.Response.Body))
	if err != nil || strings.Contains(string(decoded), subscriptionLine) {
		return Keep()
	}
	changed := base64.StdEncoding.EncodeToString([]byte(strings.TrimRight(string(decoded), "\n") + "\n" + subscriptionLine))
	return Modify(ResponseChange{Body: Bytes([]byte(changed))})
}

// teapot answers GET /server/status?teapot=1 itself and lets every other request go on.
func teapot(c SeamCall) SeamReply {
	if q, err := url.ParseQuery(c.Request.Query); err == nil && q.Get("teapot") == "1" {
		return Respond(http.StatusTeapot, map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}}, []byte("I am a teapot, said the plugin\n"))
	}
	return Continue(nil)
}
