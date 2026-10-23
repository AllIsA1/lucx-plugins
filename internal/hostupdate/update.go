// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Package hostupdate lets the plugin host replace itself with a newer build named by the signed
// host.json: it checks the list, downloads the binary, verifies its hash, runs it once to see what
// it reports, swaps it in place and starts it. A build that then fails to come up is replaced by
// the previous one. Only the host restarts: the panel and the plugin processes are not touched.
package hostupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

const (
	// DefaultBaseURL is where host.json and host.json.sig are published.
	DefaultBaseURL = "https://raw.githubusercontent.com/AllIsA1/lucx-plugins/builds"

	maxDocument   = 64 << 10
	defaultBinary = 256 << 20
	probeTimeout  = 10 * time.Second
	// CheckEvery is how often the host looks for a newer release by itself; it never applies one.
	CheckEvery = 6 * time.Hour
)

// Config places the updater; Version is what the running binary reports about itself.
type Config struct {
	Exe      string // the running binary, replaced in place
	StateDir string
	Version  string // host-N, or something else for a development build
	Arch     string
	Managed  bool // false: this host does not update itself (a development build)

	BaseURL string
	Keys    []ed25519.PublicKey
	Client  *http.Client
	// Rewrite maps a download address of host.json to the one to fetch from (tests, mirrors).
	Rewrite func(string) string
	// Exec replaces the process; the default is syscall.Exec.
	Exec func(path string, args, env []string) error
	Log  func(format string, args ...any)

	MaxBinary    int64
	MaxStarts    int           // how many starts an unconfirmed binary gets
	ConfirmAfter time.Duration // how long a start must live to count as good
}

// Status is what the interface shows.
type Status struct {
	Managed    bool      `json:"managed"`
	Version    string    `json:"version"`
	Available  string    `json:"available"`
	CheckedAt  time.Time `json:"checkedAt"`
	Busy       bool      `json:"busy"`
	Pending    string    `json:"pending"`
	RolledBack string    `json:"rolledBack"`
	Error      string    `json:"error"`
}

type pending struct {
	Version string `json:"version"`
	Starts  int    `json:"starts"`
	// Guard is set once something outside the host (the service manager's pre-start step) counts the
	// starts; the host then leaves counting to it, since it cannot count starts that die before main.
	Guard bool `json:"guard,omitempty"`
}

type state struct {
	MaxSequence int64     `json:"maxSequence"`
	DocSha256   string    `json:"docSha256"`
	CheckedAt   time.Time `json:"checkedAt"`
	Available   string    `json:"available"`
	Error       string    `json:"error"`
	Pending     *pending  `json:"pending,omitempty"`
	RolledBack  string    `json:"rolledBack"`
	Rejected    []string  `json:"rejected"`
}

// Updater runs the operations; they exclude each other.
type Updater struct {
	cfg  Config
	mu   sync.Mutex // guards st and busy
	busy bool
	st   state
	run  sync.Mutex // serialises Check, Apply and the start-up decisions
}

// New fills in the defaults and reads the saved state.
func New(cfg Config) *Updater {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	if cfg.Exec == nil {
		cfg.Exec = syscall.Exec
	}
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	if cfg.MaxBinary <= 0 {
		cfg.MaxBinary = defaultBinary
	}
	if cfg.MaxStarts <= 0 {
		cfg.MaxStarts = 3
	}
	if cfg.ConfirmAfter <= 0 {
		cfg.ConfirmAfter = 30 * time.Second
	}
	u := &Updater{cfg: cfg}
	if raw, err := os.ReadFile(u.statePath()); err == nil {
		_ = json.Unmarshal(raw, &u.st)
	}
	return u
}

func (u *Updater) statePath() string { return filepath.Join(u.cfg.StateDir, "update.json") }

// save persists the state; the caller holds u.mu.
func (u *Updater) save() {
	raw, err := json.MarshalIndent(&u.st, "", "  ")
	if err == nil {
		err = writeAtomic(u.statePath(), append(raw, '\n'), 0o644)
	}
	if err != nil {
		u.cfg.Log("cannot save the update state: %v", err)
	}
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Status reports the installed and available releases and what is going on.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := Status{
		Managed: u.cfg.Managed, Version: u.cfg.Version, Available: u.st.Available, CheckedAt: u.st.CheckedAt,
		Busy: u.busy, RolledBack: u.st.RolledBack, Error: u.st.Error,
	}
	if u.st.Pending != nil {
		s.Pending = u.st.Pending.Version
	}
	return s
}

var errBusy = errors.New("an update is already running")

// begin takes the exclusive right to run an operation, or says why not.
func (u *Updater) begin() error {
	if !u.cfg.Managed {
		return errors.New("this plugin system build does not update itself")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.busy {
		return errBusy
	}
	u.busy = true
	return nil
}

func (u *Updater) end() {
	u.mu.Lock()
	u.busy = false
	u.mu.Unlock()
}

// fail records why an operation failed and returns the error.
func (u *Updater) fail(err error) error {
	u.mu.Lock()
	u.st.Error = err.Error()
	u.save()
	u.mu.Unlock()
	return err
}

func (u *Updater) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return raw, nil
}

// document fetches host.json, verifies and parses it and applies the anti-rollback rules.
func (u *Updater) document(ctx context.Context) (*builds.HostIndex, error) {
	base := strings.TrimSuffix(u.cfg.BaseURL, "/")
	data, err := u.fetch(ctx, base+"/host.json", maxDocument)
	if err != nil {
		return nil, err
	}
	sig, err := u.fetch(ctx, base+"/host.json.sig", 4096)
	if err != nil {
		return nil, err
	}
	if err := builds.VerifyHost(data, strings.TrimSpace(string(sig)), u.cfg.Keys); err != nil {
		return nil, fmt.Errorf("the published host list is refused: %w", err)
	}
	doc, err := builds.ParseHost(data)
	if err != nil {
		return nil, fmt.Errorf("the published host list is refused: %w", err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case doc.Sequence < u.st.MaxSequence:
		return nil, errors.New("the published host list went backwards and was ignored")
	case doc.Sequence == u.st.MaxSequence && u.st.DocSha256 != "" && hash != u.st.DocSha256:
		return nil, errors.New("the published host list changed without a new sequence and was ignored")
	}
	u.st.MaxSequence, u.st.DocSha256 = doc.Sequence, hash
	return doc, nil
}

func (u *Updater) now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Check looks for a newer release. It changes nothing but what the interface shows.
func (u *Updater) Check(ctx context.Context) error {
	if err := u.begin(); err != nil {
		return err
	}
	defer u.end()
	return u.check(ctx)
}

func (u *Updater) check(ctx context.Context) error {
	doc, err := u.document(ctx)
	if err != nil {
		return u.fail(err)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.st.CheckedAt, u.st.Error = u.now(), ""
	u.st.Available = ""
	if builds.HostNewer(doc.Version, u.cfg.Version) && !slices.Contains(u.st.Rejected, doc.Version) {
		u.st.Available = doc.Version
	}
	u.save()
	return nil
}

// Apply installs the newest release if it is newer than this one. On success the new binary is in
// place and marked pending; call Restart to run it.
func (u *Updater) Apply(ctx context.Context) error {
	if err := u.begin(); err != nil {
		return err
	}
	defer u.end()
	doc, err := u.document(ctx)
	if err != nil {
		return u.fail(err)
	}
	if !builds.HostNewer(doc.Version, u.cfg.Version) {
		u.mu.Lock()
		u.st.CheckedAt, u.st.Available = u.now(), ""
		u.save()
		u.mu.Unlock()
		return u.fail(errors.New("the plugin system is already up to date"))
	}
	u.mu.Lock()
	rejected := slices.Contains(u.st.Rejected, doc.Version)
	u.mu.Unlock()
	if rejected {
		return u.fail(fmt.Errorf("%s failed to start on this host before and is not offered again", doc.Version))
	}
	bin, ok := doc.Binary(u.cfg.Arch)
	if !ok {
		return u.fail(fmt.Errorf("no build for %s", u.cfg.Arch))
	}
	if err := u.install(ctx, doc.Version, bin); err != nil {
		return u.fail(err)
	}
	u.mu.Lock()
	u.st.Pending, u.st.Available, u.st.CheckedAt, u.st.Error, u.st.RolledBack = &pending{Version: doc.Version}, "", u.now(), "", ""
	u.save()
	u.mu.Unlock()
	return nil
}

func (u *Updater) install(ctx context.Context, version string, bin builds.HostBinary) error {
	url := bin.URL
	if u.cfg.Rewrite != nil {
		url = u.cfg.Rewrite(url)
	}
	dir := filepath.Dir(u.cfg.Exe)
	tmp, err := os.CreateTemp(dir, ".lucx-host-")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", u.cfg.Exe, err)
	}
	defer os.Remove(tmp.Name())
	if err := u.download(ctx, url, bin.SHA256, tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if err := probe(ctx, tmp.Name(), version); err != nil {
		return err
	}
	if err := copyFile(u.cfg.Exe, u.cfg.Exe+".prev"); err != nil {
		return fmt.Errorf("cannot keep the previous binary: %w", err)
	}
	return os.Rename(tmp.Name(), u.cfg.Exe)
}

func (u *Updater) download(ctx context.Context, url, wantSha string, dst *os.File) error {
	defer dst.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := u.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(resp.Body, u.cfg.MaxBinary+1))
	if err != nil {
		return err
	}
	if n > u.cfg.MaxBinary {
		return fmt.Errorf("the download is larger than %d bytes", u.cfg.MaxBinary)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSha {
		return fmt.Errorf("the download does not match the signed hash (got %s)", got)
	}
	return dst.Sync()
}

// probe runs the new binary once: a build that cannot print its own version is not installed.
func probe(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-v").Output()
	if err != nil {
		return fmt.Errorf("the new binary does not run: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != version {
		return fmt.Errorf("the new binary reports %q, expected %s", got, version)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".lucx-host-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// Restart replaces this process with the binary on disk, keeping the process id so the service
// manager sees nothing. The caller has already stopped serving.
func (u *Updater) Restart() error {
	return u.cfg.Exec(u.cfg.Exe, os.Args, os.Environ())
}

// AtStart is called first thing at start-up. A binary that was just installed gets MaxStarts
// chances to prove itself (Confirm); one that keeps failing is replaced by the previous binary,
// which is then started, and its release is never offered again. rolledBack tells the caller.
func (u *Updater) AtStart() (rolledBack bool, err error) {
	u.run.Lock()
	defer u.run.Unlock()
	u.mu.Lock()
	p := u.st.Pending
	if p == nil {
		u.mu.Unlock()
		return false, nil
	}
	if p.Version != u.cfg.Version {
		// Another binary than the one that was installed is running (put back by hand, or after a
		// rollback): the mark means nothing.
		u.st.Pending = nil
		u.save()
		u.mu.Unlock()
		return false, nil
	}
	if p.Guard {
		u.mu.Unlock()
		return false, nil
	}
	p.Starts++
	if p.Starts <= u.cfg.MaxStarts {
		u.save()
		u.mu.Unlock()
		return false, nil
	}
	u.mu.Unlock()

	if err := copyFile(u.cfg.Exe+".prev", u.cfg.Exe); err != nil {
		return false, fmt.Errorf("%s keeps failing to start and the previous binary cannot be restored: %w", p.Version, err)
	}
	u.mu.Lock()
	u.st.Pending, u.st.RolledBack = nil, p.Version
	u.st.Rejected = append(u.st.Rejected, p.Version)
	u.st.Error = fmt.Sprintf("%s did not start properly and was rolled back", p.Version)
	u.save()
	u.mu.Unlock()
	u.cfg.Log("%s did not start %d times in a row; the previous binary is back", p.Version, u.cfg.MaxStarts)
	return true, u.Restart()
}

// Confirm says the running binary has proved itself; the host calls it after ConfirmAfter.
func (u *Updater) Confirm() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.st.Pending != nil && u.st.Pending.Version == u.cfg.Version {
		u.st.Pending = nil
		u.save()
	}
}

// ConfirmAfter is how long a start must live before it counts as good.
func (u *Updater) ConfirmAfter() time.Duration { return u.cfg.ConfirmAfter }

// Watch checks for a newer release now and then until ctx ends; it never applies one.
func (u *Updater) Watch(ctx context.Context, first time.Duration) {
	if !u.cfg.Managed {
		return
	}
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := u.Check(ctx); err != nil && !errors.Is(err, errBusy) {
			u.cfg.Log("update check: %v", err)
		}
		timer.Reset(CheckEvery)
	}
}

// Guard is the pre-start step of the service manager, run by a separate program before every start
// of the host. It counts the starts of a freshly installed binary from outside, so a binary that
// cannot reach its own code is still replaced by the previous one after MaxStarts failed starts. It
// reports whether it rolled back; the caller (and the service manager) starts the host either way.
func Guard(cfg Config) (rolledBack bool, err error) {
	if cfg.MaxStarts <= 0 {
		cfg.MaxStarts = 3
	}
	u := New(cfg)
	u.run.Lock()
	defer u.run.Unlock()
	u.mu.Lock()
	p := u.st.Pending
	u.mu.Unlock()
	if p == nil {
		return false, nil
	}
	running := binaryVersion(cfg.Exe)
	u.mu.Lock()
	if running != "" && running != p.Version {
		u.st.Pending = nil
		u.save()
		u.mu.Unlock()
		return false, nil
	}
	p.Guard = true
	p.Starts++
	if p.Starts <= cfg.MaxStarts {
		u.save()
		u.mu.Unlock()
		return false, nil
	}
	u.mu.Unlock()
	if err := copyFile(cfg.Exe+".prev", cfg.Exe); err != nil {
		return false, fmt.Errorf("%s keeps failing to start and the previous binary cannot be restored: %w", p.Version, err)
	}
	u.mu.Lock()
	u.st.Pending, u.st.RolledBack = nil, p.Version
	u.st.Rejected = append(u.st.Rejected, p.Version)
	u.st.Error = fmt.Sprintf("%s did not start properly and was rolled back", p.Version)
	u.save()
	u.mu.Unlock()
	return true, nil
}

// binaryVersion asks a binary for its version; empty when it cannot say.
func binaryVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-v").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
