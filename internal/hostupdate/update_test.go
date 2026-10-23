// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

// script is a stand-in for a host binary: it answers -v the way the real one does.
func script(version string) []byte {
	return []byte("#!/bin/sh\n# " + version + "\necho " + version + "\n")
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// release is what the fake download server publishes.
type release struct {
	version string
	amd64   []byte
	arm64   []byte
}

type world struct {
	t      *testing.T
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	srv    *httptest.Server
	mu     sync.Mutex
	doc    []byte
	sig    string
	files  map[string][]byte // path -> content
	hits   map[string]int
	down   bool
	exe    string
	dir    string
	execed []string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, pub: pub, priv: priv, files: map[string][]byte{}, hits: map[string]int{}, dir: t.TempDir()}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.hits[r.URL.Path]++
		if w.down {
			http.Error(rw, "down", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/builds/host.json":
			_, _ = rw.Write(w.doc)
		case "/builds/host.json.sig":
			_, _ = rw.Write([]byte(w.sig + "\n"))
		default:
			if body, ok := w.files[r.URL.Path]; ok {
				_, _ = rw.Write(body)
				return
			}
			http.NotFound(rw, r)
		}
	}))
	t.Cleanup(w.srv.Close)
	w.exe = filepath.Join(w.dir, "lucx-plugin-host")
	if err := os.WriteFile(w.exe, script("host-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	return w
}

var seq int64

// publish makes rel the current release; the document gets the next sequence number unless a test
// overrides it afterwards with publishRaw.
func (w *world) publish(rel release) {
	w.t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	seq++
	doc := &builds.HostIndex{Schema: builds.HostSchemaVersion, Sequence: seq}
	if err := doc.Set(rel.version, map[string]string{"amd64": sum(rel.amd64), "arm64": sum(rel.arm64)}); err != nil {
		w.t.Fatal(err)
	}
	doc.Sequence = seq
	doc.GeneratedAt = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	raw, err := builds.MarshalHost(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	w.doc, w.sig = raw, builds.SignHost(w.priv, raw)
	for _, b := range doc.Binaries {
		path := strings.TrimPrefix(b.URL, "https://github.com/AllIsA1/lucx-plugins/releases/download/")
		if b.Arch == "amd64" {
			w.files["/dl/"+path] = rel.amd64
		} else {
			w.files["/dl/"+path] = rel.arm64
		}
	}
}

func (w *world) updater(version string) *Updater {
	w.t.Helper()
	return New(Config{
		Exe: w.exe, StateDir: filepath.Join(w.dir, "state"), Version: version, Arch: "amd64", Managed: true,
		BaseURL: w.srv.URL + "/builds", Keys: []ed25519.PublicKey{w.pub},
		Rewrite: func(u string) string {
			return strings.Replace(u, "https://github.com/AllIsA1/lucx-plugins/releases/download/", w.srv.URL+"/dl/", 1)
		},
		Exec: func(path string, args, env []string) error { w.execed = append(w.execed, path); return nil },
		Log:  func(format string, args ...any) { w.t.Logf(format, args...) },
	})
}

func (w *world) exeContent() string {
	b, err := os.ReadFile(w.exe)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

func next(version string) release {
	return release{version: version, amd64: script(version), arm64: append(script(version), '\n')}
}

func TestCheckFindsOnlyANewerRelease(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-3")
	for _, c := range []struct {
		published string
		want      string
	}{{"host-3", ""}, {"host-2", ""}, {"host-4", "host-4"}} {
		w.publish(next(c.published))
		if err := u.Check(context.Background()); err != nil {
			t.Fatalf("Check with %s published: %v", c.published, err)
		}
		if got := u.Status().Available; got != c.want {
			t.Errorf("%s published, installed host-3: available = %q, want %q", c.published, got, c.want)
		}
	}
	if st := u.Status(); st.Version != "host-3" || st.CheckedAt.IsZero() || st.Error != "" || !st.Managed {
		t.Errorf("status = %+v", st)
	}
}

func TestCheckRefusesAListThatDoesNotVerify(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	w.sig = builds.SignHost(func() ed25519.PrivateKey { _, p, _ := ed25519.GenerateKey(rand.Reader); return p }(), w.doc)
	if err := u.Check(context.Background()); err == nil {
		t.Fatal("a list signed by a foreign key was accepted")
	}
	if st := u.Status(); st.Available != "" || st.Error == "" {
		t.Errorf("status = %+v, want nothing available and the reason", st)
	}

	w.publish(next("host-2"))
	w.doc = []byte(strings.Replace(string(w.doc), "host-2", "host-9", 1)) // tampered after signing
	if err := u.Check(context.Background()); err == nil {
		t.Error("a tampered list was accepted")
	}
	w.sig = builds.Sign(w.priv, w.doc) // a builds-list signature is not a host signature
	if err := u.Check(context.Background()); err == nil {
		t.Error("a signature made for builds.json was accepted")
	}
}

func TestCheckNeverGoesBackwards(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	oldDoc, oldSig := w.doc, w.sig
	w.publish(next("host-3"))
	if err := u.Check(context.Background()); err != nil || u.Status().Available != "host-3" {
		t.Fatalf("setup: %v %+v", err, u.Status())
	}
	w.doc, w.sig = oldDoc, oldSig // an attacker replays an older, validly signed list
	if err := u.Check(context.Background()); err == nil {
		t.Error("an older list was accepted")
	}
	if got := u.Status().Available; got != "host-3" {
		t.Errorf("available = %q after a replay, want host-3 kept", got)
	}

	// Same sequence, different content: also refused.
	w.publish(next("host-4"))
	_ = u.Check(context.Background())
	sameSeq := &builds.HostIndex{Schema: 1, Sequence: seq}
	_ = sameSeq.Set("host-5", map[string]string{"amd64": sum(script("x")), "arm64": sum(script("y"))})
	sameSeq.Sequence = seq
	raw, _ := builds.MarshalHost(sameSeq)
	w.doc, w.sig = raw, builds.SignHost(w.priv, raw)
	if err := u.Check(context.Background()); err == nil {
		t.Error("a different list with the sequence already accepted was taken")
	}
}

func TestCheckWithoutTheNetworkKeepsWhatItKnew(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	if err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.down = true
	if err := u.Check(context.Background()); err == nil {
		t.Fatal("an unreachable server was not reported")
	}
	if st := u.Status(); st.Available != "host-2" || st.Error == "" {
		t.Errorf("status = %+v, want host-2 still offered and the error shown", st)
	}
}

func TestApplyReplacesTheBinaryKeepsThePreviousOneAndMarksItPending(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	if err := u.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := w.exeContent(); got != string(script("host-2")) {
		t.Errorf("binary = %q", got)
	}
	if prev, err := os.ReadFile(w.exe + ".prev"); err != nil || string(prev) != string(script("host-1")) {
		t.Errorf(".prev = %q, %v; want the old binary", prev, err)
	}
	if st, err := os.Stat(w.exe); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, %v; want 0755", st.Mode(), err)
	}
	if left, _ := filepath.Glob(filepath.Join(w.dir, ".lucx-host-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if st := u.Status(); st.Pending != "host-2" {
		t.Errorf("status = %+v, want host-2 pending until it proves itself", st)
	}
}

func TestApplyRefusesWhatItCannotTrustAndTouchesNothing(t *testing.T) {
	cases := map[string]func(w *world){
		"nothing newer": func(w *world) { w.publish(next("host-1")) },
		"binary does not match the signed hash": func(w *world) {
			w.publish(next("host-2"))
			w.files["/dl/host-2/lucx-plugin-host-linux-amd64"] = script("host-2-evil")
		},
		"binary that cannot run": func(w *world) {
			w.publish(release{version: "host-2", amd64: []byte("not an executable"), arm64: script("host-2")})
		},
		"binary that reports another version": func(w *world) {
			w.publish(release{version: "host-2", amd64: script("host-7"), arm64: script("host-2")})
		},
		"download fails": func(w *world) {
			w.publish(next("host-2"))
			delete(w.files, "/dl/host-2/lucx-plugin-host-linux-amd64")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			u := w.updater("host-1")
			setup(w)
			if err := u.Apply(context.Background()); err == nil {
				t.Fatal("Apply succeeded")
			}
			if w.exeContent() != string(script("host-1")) {
				t.Errorf("the running binary was changed: %q", w.exeContent())
			}
			if _, err := os.Stat(w.exe + ".prev"); err == nil {
				t.Error(".prev written for an update that did not happen")
			}
			if left, _ := filepath.Glob(filepath.Join(w.dir, ".lucx-host-*")); len(left) != 0 {
				t.Errorf("temporary files left: %v", left)
			}
			if st := u.Status(); st.Pending != "" || st.Error == "" || st.Busy {
				t.Errorf("status = %+v, want an error shown, nothing pending, not busy", st)
			}
		})
	}
}

func TestOnlyOneUpdateRunsAtATime(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	release := make(chan struct{})
	w.srv.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dl/") {
			<-release
		}
		w.mu.Lock()
		body := w.files[r.URL.Path]
		doc, sig := w.doc, w.sig
		w.mu.Unlock()
		switch r.URL.Path {
		case "/builds/host.json":
			_, _ = rw.Write(doc)
		case "/builds/host.json.sig":
			_, _ = rw.Write([]byte(sig))
		default:
			_, _ = rw.Write(body)
		}
	})
	done := make(chan error, 1)
	go func() { done <- u.Apply(context.Background()) }()
	deadline := time.Now().Add(3 * time.Second)
	for !u.Status().Busy {
		if time.Now().After(deadline) {
			t.Fatal("never became busy")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := u.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("second Apply = %v, want it refused as already running", err)
	}
	if err := u.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("Check during an update = %v, want it refused", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the first Apply: %v", err)
	}
}

// start simulates the new binary starting under systemd: a fresh process with the new version.
func (w *world) start(version string) (*Updater, bool) {
	u := w.updater(version)
	rolled, err := u.AtStart()
	if err != nil {
		w.t.Fatalf("AtStart: %v", err)
	}
	return u, rolled
}

func TestANewBinaryThatKeepsCrashingIsRolledBack(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	if err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, rolled := w.start("host-2"); rolled {
			t.Fatalf("rolled back at start %d, the new binary gets three tries", i)
		}
	}
	back, rolled := w.start("host-2")
	if !rolled {
		t.Fatal("the fourth unconfirmed start did not roll back")
	}
	if w.exeContent() != string(script("host-1")) {
		t.Errorf("binary after rollback = %q, want the previous one", w.exeContent())
	}
	if len(w.execed) != 1 || w.execed[0] != w.exe {
		t.Errorf("exec calls = %v, want the restored binary started once", w.execed)
	}
	st := back.Status()
	if st.Pending != "" || st.RolledBack != "host-2" || st.Error == "" {
		t.Errorf("status = %+v, want the rollback explained", st)
	}

	// The bad release is not offered again.
	old := w.updater("host-1")
	if err := old.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := old.Status().Available; got != "" {
		t.Errorf("available = %q: a release that failed to start was offered again", got)
	}
	w.publish(next("host-3"))
	if err := old.Check(context.Background()); err != nil || old.Status().Available != "host-3" {
		t.Errorf("a later release must still be offered: %v %+v", err, old.Status())
	}
}

func TestAConfirmedBinaryIsNeverRolledBack(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	if err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	running, _ := w.start("host-2")
	running.Confirm()
	if running.Status().Pending != "" {
		t.Error("still pending after Confirm")
	}
	for i := 0; i < 10; i++ {
		if _, rolled := w.start("host-2"); rolled {
			t.Fatal("a confirmed binary was rolled back")
		}
	}
	if w.exeContent() != string(script("host-2")) {
		t.Error("binary changed")
	}
}

func TestAStalePendingMarkIsDropped(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	if err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The operator put the old binary back by hand: it starts as host-1 while host-2 is pending.
	again, rolled := w.start("host-1")
	if rolled || again.Status().Pending != "" {
		t.Errorf("rolled=%v status=%+v: a pending mark for another version must just go", rolled, again.Status())
	}
}

func TestRestartRunsTheBinaryInPlace(t *testing.T) {
	w := newWorld(t)
	u := w.updater("host-1")
	if err := u.Restart(); err != nil {
		t.Fatal(err)
	}
	if len(w.execed) != 1 || w.execed[0] != w.exe {
		t.Errorf("exec = %v, want %s", w.execed, w.exe)
	}
}

func TestUnmanagedHostsOfferNothing(t *testing.T) {
	w := newWorld(t)
	u := New(Config{Exe: w.exe, StateDir: filepath.Join(w.dir, "state"), Version: "dev", Arch: "amd64", BaseURL: w.srv.URL + "/builds", Keys: []ed25519.PublicKey{w.pub}})
	if st := u.Status(); st.Managed {
		t.Errorf("a dev build reports itself managed: %+v", st)
	}
	if err := u.Check(context.Background()); err == nil {
		t.Error("an unmanaged host checked for updates")
	}
	if err := u.Apply(context.Background()); err == nil {
		t.Error("an unmanaged host replaced itself")
	}
	if w.hits["/builds/host.json"] != 0 {
		t.Error("an unmanaged host went to the network")
	}
}

// install puts host-2 in place as an update would and returns the world ready for guard runs.
func installed(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	u := w.updater("host-1")
	w.publish(next("host-2"))
	if err := u.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) guard() (bool, error) {
	return Guard(Config{Exe: w.exe, StateDir: filepath.Join(w.dir, "state"), MaxStarts: 3})
}

func TestGuardRollsBackABinaryThatNeverReachesItsOwnCode(t *testing.T) {
	w := installed(t)
	// The new binary prints its version for the probe but dies as soon as it really starts: only
	// something outside it can count those starts.
	broken := []byte("#!/bin/sh\nif [ \"$1\" = \"-v\" ]; then echo host-2; exit 0; fi\nexit 1\n")
	if err := os.WriteFile(w.exe, broken, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if rolled, err := w.guard(); err != nil || rolled {
			t.Fatalf("start %d: rolled=%v err=%v; the new binary gets three tries", i, rolled, err)
		}
	}
	rolled, err := w.guard()
	if err != nil || !rolled {
		t.Fatalf("start 4: rolled=%v err=%v, want a rollback", rolled, err)
	}
	if w.exeContent() != string(script("host-1")) {
		t.Errorf("binary after rollback = %q, want the previous one", w.exeContent())
	}
	st := w.updater("host-1").Status()
	if st.Pending != "" || st.RolledBack != "host-2" || st.Error == "" {
		t.Errorf("status = %+v, want the rollback explained", st)
	}
	old := w.updater("host-1")
	w.publish(next("host-2"))
	if err := old.Check(context.Background()); err != nil || old.Status().Available != "" {
		t.Errorf("the rolled-back release is offered again: %v %+v", err, old.Status())
	}
}

func TestGuardCountsABinaryThatCannotEvenReportItsVersion(t *testing.T) {
	w := installed(t)
	if err := os.WriteFile(w.exe, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, _ = w.guard()
	}
	if rolled, err := w.guard(); err != nil || !rolled {
		t.Errorf("rolled=%v err=%v: a binary that does not even run must be replaced too", rolled, err)
	}
}

func TestGuardLeavesAConfirmedOrUnrelatedBinaryAlone(t *testing.T) {
	w := installed(t)
	running, _ := w.start("host-2")
	running.Confirm()
	for i := 0; i < 10; i++ {
		if rolled, err := w.guard(); err != nil || rolled {
			t.Fatalf("a confirmed binary was touched: rolled=%v err=%v", rolled, err)
		}
	}
	if w.exeContent() != string(script("host-2")) {
		t.Error("binary changed")
	}

	// Nothing installed by an update: nothing to guard.
	empty := newWorld(t)
	if rolled, err := empty.guard(); err != nil || rolled {
		t.Errorf("a host that never updated: rolled=%v err=%v", rolled, err)
	}
}

func TestGuardDropsAMarkThatBelongsToAnotherBinary(t *testing.T) {
	w := installed(t)
	if err := os.WriteFile(w.exe, script("host-1"), 0o755); err != nil { // put back by hand
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if rolled, err := w.guard(); err != nil || rolled {
			t.Fatalf("rolled=%v err=%v for a binary the update did not install", rolled, err)
		}
	}
	if st := w.updater("host-1").Status(); st.Pending != "" {
		t.Errorf("status = %+v, the stale mark should be gone", st)
	}
}

func TestOnceTheGuardCountsTheBinaryDoesNotCountAgain(t *testing.T) {
	w := installed(t)
	if _, err := w.guard(); err != nil {
		t.Fatal(err)
	}
	// With the guard in charge, the binary's own start-up must not double the count.
	for i := 0; i < 5; i++ {
		if _, rolled := w.start("host-2"); rolled {
			t.Fatal("AtStart rolled back although the guard is the one counting")
		}
	}
	for i := 0; i < 2; i++ {
		if rolled, _ := w.guard(); rolled {
			t.Fatalf("rolled back at guard run %d, only the guard's own count matters", i+2)
		}
	}
	if rolled, _ := w.guard(); !rolled {
		t.Error("the guard did not roll back after its own fourth count")
	}
}

func TestInstallFreshPlacesTheCurrentReleaseWhereNothingIsInstalled(t *testing.T) {
	w := newWorld(t)
	if err := os.Remove(w.exe); err != nil {
		t.Fatal(err)
	}
	w.publish(next("host-5"))
	u := w.updater("")
	if err := u.InstallFresh(context.Background()); err != nil {
		t.Fatalf("InstallFresh: %v", err)
	}
	if w.exeContent() != string(script("host-5")) {
		t.Errorf("binary = %q", w.exeContent())
	}
	if st, err := os.Stat(w.exe); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, %v; want 0755", st.Mode(), err)
	}
	if _, err := os.Stat(w.exe + ".prev"); err == nil {
		t.Error(".prev written although there was nothing before")
	}
	if st := u.Status(); st.Pending != "" || st.Error != "" {
		t.Errorf("status = %+v: a fresh install has nothing to roll back to and nothing to confirm", st)
	}
	if left, _ := filepath.Glob(filepath.Join(w.dir, ".lucx-host-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestInstallFreshRefusesWhatApplyRefusesAndNeverOverwrites(t *testing.T) {
	cases := map[string]func(w *world){
		"a binary already there": func(w *world) { w.publish(next("host-5")) },
		"a hash that does not match": func(w *world) {
			_ = os.Remove(w.exe)
			w.publish(next("host-5"))
			w.files["/dl/host-5/lucx-plugin-host-linux-amd64"] = script("host-5-evil")
		},
		"a binary that cannot run": func(w *world) {
			_ = os.Remove(w.exe)
			w.publish(release{version: "host-5", amd64: []byte("not an executable"), arm64: script("host-5")})
		},
		"a list the key did not sign": func(w *world) {
			_ = os.Remove(w.exe)
			w.publish(next("host-5"))
			w.sig = builds.SignHost(func() ed25519.PrivateKey { _, p, _ := ed25519.GenerateKey(rand.Reader); return p }(), w.doc)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			setup(w)
			before, beforeErr := os.ReadFile(w.exe)
			if err := w.updater("").InstallFresh(context.Background()); err == nil {
				t.Fatal("InstallFresh succeeded")
			}
			after, afterErr := os.ReadFile(w.exe)
			if string(before) != string(after) || (beforeErr == nil) != (afterErr == nil) {
				t.Errorf("the file at the binary's path changed: %q -> %q", before, after)
			}
			if left, _ := filepath.Glob(filepath.Join(w.dir, ".lucx-host-*")); len(left) != 0 {
				t.Errorf("temporary files left: %v", left)
			}
		})
	}
}
