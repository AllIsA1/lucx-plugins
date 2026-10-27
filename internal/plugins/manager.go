// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/logx"
)

const (
	stagingDirName    = ".staging"
	maxRedirects      = 3
	defaultStagingTTL = 15 * time.Minute

	// MaxPendingPreviews bounds disk used by unconfirmed downloads.
	MaxPendingPreviews = 5
)

var (
	ErrAlreadyInstalled    = errors.New("plugin is already installed")
	ErrPermissionsMismatch = errors.New("accepted permissions do not match the plugin manifest")
	ErrUnknownToken        = errors.New("unknown or expired install token")
	ErrManifestMismatch    = errors.New("the plugin manifest differs from the one that was approved")
	ErrScopeNotAllowed     = errors.New("the plugin scope does not allow installing it on a node")
	ErrReplaceConflict     = errors.New("another plugin already replaces that page")
	ErrRequirementMissing  = errors.New("a plugin this one requires is not installed and turned on")
	ErrRequiredBy          = errors.New("other plugins need this one")
	ErrNotMasterManaged    = errors.New("the plugin was installed on this node by hand and is not managed by the master")
	ErrAlreadyCurrent      = errors.New("the plugin already runs that commit")
)

// Config wires a Manager to the filesystem, the network and the panel version.
type Config struct {
	CodeDir      string
	DataDir      string
	RegistryPath string
	ArchiveBase  string
	HTTPClient   *http.Client
	Limits       Limits
	PanelVersion string
	Now          func() time.Time
	StagingTTL   time.Duration
	Runner       Runner
	KeyPath      string
	PanelURL     func() string

	// Catalog: the keys that may sign it (none = no catalog), where it is published, how old a cache may get and where it is kept.
	CatalogKeys      []ed25519.PublicKey
	CatalogBase      string
	CatalogMaxAge    time.Duration
	CatalogCachePath string
}

// Preview describes a downloaded, unpacked and validated plugin awaiting confirmation.
type Preview struct {
	Token     string    `json:"token"`
	Source    Source    `json:"source"`
	Commit    string    `json:"commit"`
	Manifest  *Manifest `json:"manifest"`
	Dangerous []string  `json:"dangerous"`
	Installed bool      `json:"installed"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Catalog is listed, unlisted or unavailable: whether the signed catalog vouches for this exact commit.
	Catalog string `json:"catalog,omitempty"`
}

type stagedPlugin struct {
	dir            string
	preview        Preview
	manifestSHA256 string
}

// Manager installs, toggles and removes plugins. It never executes plugin code.
type Manager struct {
	cfg    Config
	reg    *Registry
	client *http.Client

	mu     sync.Mutex
	staged map[string]*stagedPlugin
	proc   ProcessController
	signer *TokenSigner
	cat    catalogCache
}

// ProcessController stops a plugin's process; Uninstall calls it first.
type ProcessController interface {
	StopPlugin(ctx context.Context, id string) error
}

// SetProcessController registers who stops processes before code is deleted.
func (m *Manager) SetProcessController(c ProcessController) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proc = c
}

// NewManager prepares the plugin directories and loads the registry. A corrupt
// registry is returned as an error so the caller can run without plugins.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.CodeDir == "" || cfg.DataDir == "" || cfg.RegistryPath == "" {
		return nil, errors.New("plugins: CodeDir, DataDir and RegistryPath are required")
	}
	base, err := ValidateArchiveBase(cfg.ArchiveBase)
	if err != nil {
		return nil, err
	}
	cfg.ArchiveBase = base
	if cfg.Limits == (Limits{}) {
		cfg.Limits = DefaultLimits
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.StagingTTL <= 0 {
		cfg.StagingTTL = defaultStagingTTL
	}
	reg, err := OpenRegistry(cfg.RegistryPath)
	if err != nil {
		return nil, err
	}
	if cfg.CatalogBase == "" {
		cfg.CatalogBase = DefaultCatalogBase
	} else if v, err := ValidateArchiveBase(cfg.CatalogBase); err != nil {
		return nil, fmt.Errorf("catalog base: %w", err)
	} else {
		cfg.CatalogBase = v
	}
	if cfg.CatalogMaxAge <= 0 {
		cfg.CatalogMaxAge = defaultCatalogMaxAge
	}
	if cfg.CatalogCachePath == "" {
		cfg.CatalogCachePath = filepath.Join(filepath.Dir(cfg.RegistryPath), "plugins-catalog.json")
	}
	if cfg.KeyPath == "" {
		cfg.KeyPath = filepath.Join(filepath.Dir(cfg.RegistryPath), "plugins.key")
	}
	key, err := LoadOrCreateKey(cfg.KeyPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.CodeDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Chmod(cfg.CodeDir, 0o755); err != nil {
		return nil, err
	}
	staging := filepath.Join(cfg.CodeDir, stagingDirName)
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, reg: reg, client: newDownloadClient(cfg.HTTPClient), staged: map[string]*stagedPlugin{}, signer: NewTokenSigner(key)}, nil
}

// IssueToken returns the API token a plugin process presents to the panel.
func (m *Manager) IssueToken(e Entry) string { return m.signer.Issue(e) }

// VerifyToken resolves a presented plugin token to its current registry entry.
func (m *Manager) VerifyToken(token string) (Entry, bool) {
	return m.signer.Verify(token, m.reg.Get)
}

func newDownloadClient(base *http.Client) *http.Client {
	c := &http.Client{Timeout: 5 * time.Minute}
	if base != nil {
		clone := *base
		c = &clone
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("too many redirects")
		}
		if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
			return fmt.Errorf("redirect to %s refused: archives must come from the configured host", req.URL.Host)
		}
		return nil
	}
	return c
}

// ArchiveBase returns the effective download base: setting, config, default.
func (m *Manager) ArchiveBase() string {
	if v := m.reg.ArchiveBase(); v != "" {
		return v
	}
	return m.cfg.ArchiveBase
}

// CustomArchiveBase returns the operator-set mirror, "" when none is set.
func (m *Manager) CustomArchiveBase() string {
	return m.reg.ArchiveBase()
}

// SetArchiveBase stores a mirror base; "" restores the configured default.
func (m *Manager) SetArchiveBase(raw string) error {
	return m.reg.SetArchiveBase(raw)
}

// Get returns one installed plugin.
func (m *Manager) Get(id string) (Entry, bool) { return m.reg.Get(id) }

// List returns every installed plugin.
func (m *Manager) List() []Entry {
	return m.reg.List()
}

// Preview downloads and validates a plugin without installing it.
func (m *Manager) Preview(ctx context.Context, rawURL string) (*Preview, error) {
	src, err := ParseSource(rawURL)
	if err != nil {
		return nil, err
	}
	p, err := m.previewSource(ctx, src)
	if err != nil {
		return nil, err
	}
	p.Catalog = m.catalogListing(p)
	return p, nil
}

func (m *Manager) previewSource(ctx context.Context, src Source) (*Preview, error) {
	m.mu.Lock()
	m.sweepLocked()
	m.mu.Unlock()

	token, err := newToken()
	if err != nil {
		return nil, err
	}
	stageDir := filepath.Join(m.cfg.CodeDir, stagingDirName, token)
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		return nil, err
	}
	preview, manifestSHA, err := m.stage(ctx, src, stageDir)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return nil, err
	}
	if err := m.replaceConflict(preview.Manifest); err != nil {
		_ = os.RemoveAll(stageDir)
		return nil, err
	}
	preview.Token = token
	preview.ExpiresAt = m.cfg.Now().Add(m.cfg.StagingTTL)
	if _, ok := m.reg.Get(preview.Manifest.ID); ok {
		preview.Installed = true
	}
	m.mu.Lock()
	m.staged[token] = &stagedPlugin{dir: stageDir, preview: *preview, manifestSHA256: manifestSHA}
	m.evictOldestLocked()
	m.mu.Unlock()
	return preview, nil
}

func (m *Manager) stage(ctx context.Context, src Source, stageDir string) (*Preview, string, error) {
	body, err := m.download(ctx, src.ArchiveURL(m.ArchiveBase()))
	if err != nil {
		return nil, "", err
	}
	defer body.Close()
	headerCommit, err := ExtractTarGz(body, stageDir, m.cfg.Limits)
	if err != nil {
		return nil, "", err
	}
	commit, err := resolveCommit(src.Ref, headerCommit)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(filepath.Join(stageDir, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("the repository has no %s at its root", ManifestFile)
	}
	if err != nil {
		return nil, "", err
	}
	manifest, err := ParseManifest(raw)
	if err != nil {
		return nil, "", err
	}
	if err := manifest.CompatibleWith(m.cfg.PanelVersion); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return &Preview{
		Source:    src,
		Commit:    commit,
		Manifest:  manifest,
		Dangerous: dangerousPermissions(manifest.Permissions),
	}, hex.EncodeToString(sum[:]), nil
}

func resolveCommit(ref, fromArchive string) (string, error) {
	pinned := ""
	if shaPattern.MatchString(strings.ToLower(ref)) {
		pinned = strings.ToLower(ref)
	}
	switch {
	case fromArchive != "" && pinned != "" && fromArchive != pinned:
		return "", fmt.Errorf("commit mismatch: URL pins %s but the archive is %s", pinned, fromArchive)
	case fromArchive != "":
		return fromArchive, nil
	case pinned != "":
		return pinned, nil
	}
	return "", errors.New("the archive does not carry a commit id; pin a full commit SHA in the URL")
}

func (m *Manager) download(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lucx-ui-plugins")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, errors.New("repository or ref not found (private repositories are not supported)")
	case resp.StatusCode != http.StatusOK:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	case resp.ContentLength > m.cfg.Limits.MaxArchiveBytes:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("archive is too large (%d bytes)", resp.ContentLength)
	}
	return &cappedBody{Reader: &capReader{r: resp.Body, left: m.cfg.Limits.MaxArchiveBytes}, closer: resp.Body}, nil
}

type cappedBody struct {
	io.Reader
	closer io.Closer
}

func (c *cappedBody) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	if errors.Is(err, errTooLarge) {
		return n, errors.New("archive is too large to download")
	}
	return n, err
}

func (c *cappedBody) Close() error { return c.closer.Close() }

// Install moves a previewed plugin into place after the operator confirmed
// exactly the permissions in its manifest. A token works once.
func (m *Manager) Install(token string, accepted []string) (Entry, error) {
	return m.install(token, accepted, false)
}

func (m *Manager) install(token string, accepted []string, managedByMaster bool) (Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()

	s, ok := m.staged[token]
	if !ok {
		return Entry{}, ErrUnknownToken
	}
	delete(m.staged, token)
	installed := false
	defer func() {
		if !installed {
			_ = os.RemoveAll(s.dir)
		}
	}()

	manifest := s.preview.Manifest
	if !SamePermissionSet(accepted, manifest.Permissions) {
		return Entry{}, ErrPermissionsMismatch
	}
	if _, exists := m.reg.Get(manifest.ID); exists {
		return Entry{}, ErrAlreadyInstalled
	}
	if err := m.replaceConflict(manifest); err != nil {
		return Entry{}, err
	}
	if err := m.requirementsMetLocked(manifest.Requires); err != nil {
		return Entry{}, err
	}
	target := filepath.Join(m.cfg.CodeDir, manifest.ID)
	if _, err := os.Lstat(target); err == nil {
		return Entry{}, fmt.Errorf("%w: %s already exists on disk", ErrAlreadyInstalled, target)
	}
	if err := os.Rename(s.dir, target); err != nil {
		return Entry{}, err
	}
	entry := Entry{
		ID:              manifest.ID,
		Name:            manifest.Name,
		Version:         manifest.Version,
		Type:            manifest.Type,
		Scope:           manifest.Scope,
		Source:          s.preview.Source,
		Commit:          s.preview.Commit,
		ManifestSHA256:  s.manifestSHA256,
		InstalledAt:     m.cfg.Now().UTC(),
		Permissions:     normalizePermissions(manifest.Permissions),
		ManagedByMaster: managedByMaster,
		UI:              manifest.UI,
		Seams:           manifest.Seams,
		Requires:        slices.Clone(manifest.Requires),
	}
	if err := m.reg.Put(entry); err != nil {
		_ = os.RemoveAll(target)
		return Entry{}, err
	}
	installed = true
	logx.Infof("plugins: installed %s %s (%s)", entry.ID, entry.Version, entry.Commit)
	return entry, nil
}

// PinnedInstall is what a master panel approved: one commit, one manifest and
// exactly these permissions.
type PinnedInstall struct {
	URL            string
	Commit         string
	ManifestSHA256 string
	Permissions    []string
}

// InstallPinned downloads the pinned commit itself and installs it, disabled
// and marked as master-managed, only if the manifest hash, the permission set
// and the scope are what the master approved. Nothing is trusted on its word.
func (m *Manager) InstallPinned(ctx context.Context, req PinnedInstall) (Entry, error) {
	p, err := m.stagePinned(ctx, req)
	if err != nil {
		return Entry{}, err
	}
	return m.install(p.Token, req.Permissions, true)
}

// stagePinned downloads the pinned commit and keeps it staged only if the manifest hash and the scope are what the
// master approved; the caller installs or applies it (the token), or the staging is dropped here.
func (m *Manager) stagePinned(ctx context.Context, req PinnedInstall) (*Preview, error) {
	commit := strings.TrimSpace(req.Commit)
	if !shaPattern.MatchString(commit) {
		return nil, errors.New("commit must be a full lowercase SHA: a branch or tag can move")
	}
	src, err := ParseSource(req.URL)
	if err != nil {
		return nil, err
	}
	src.Ref = commit
	p, err := m.Preview(ctx, src.String())
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	staged := m.staged[p.Token]
	m.mu.Unlock()
	switch {
	case staged == nil:
		return nil, ErrUnknownToken
	case !strings.EqualFold(staged.manifestSHA256, strings.TrimSpace(req.ManifestSHA256)):
		m.discard(p.Token)
		return nil, ErrManifestMismatch
	case p.Manifest.Scope != ScopeNode && p.Manifest.Scope != ScopeBoth:
		m.discard(p.Token)
		return nil, fmt.Errorf("%w: scope is %q", ErrScopeNotAllowed, p.Manifest.Scope)
	}
	return p, nil
}

// TakePin turns a previewed plugin into what a node must install: this panel never installs it, it only vouches for
// the exact commit, manifest and permissions the operator saw. The preview is used up. The plugin must be one a node can run.
func (m *Manager) TakePin(token string, accepted []string) (PinnedInstall, *Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.staged[token]
	if !ok {
		return PinnedInstall{}, nil, ErrUnknownToken
	}
	manifest := s.preview.Manifest
	switch {
	case !SamePermissionSet(accepted, manifest.Permissions):
		return PinnedInstall{}, nil, ErrPermissionsMismatch
	case manifest.Scope != ScopeNode && manifest.Scope != ScopeBoth:
		return PinnedInstall{}, nil, fmt.Errorf("%w: scope is %q", ErrScopeNotAllowed, manifest.Scope)
	}
	src := s.preview.Source
	src.Ref = ""
	delete(m.staged, token)
	_ = os.RemoveAll(s.dir)
	return PinnedInstall{URL: src.String(), Commit: s.preview.Commit, ManifestSHA256: s.manifestSHA256, Permissions: normalizePermissions(manifest.Permissions)}, manifest, nil
}

func (m *Manager) discard(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.staged[token]; ok {
		_ = os.RemoveAll(s.dir)
		delete(m.staged, token)
	}
}

// SetEnabled flips the enabled flag of an installed plugin.
func (m *Manager) SetEnabled(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.reg.Get(id)
	if !ok {
		return ErrNotFound
	}
	if enabled {
		if err := m.requirementsMetLocked(e.Requires); err != nil {
			return err
		}
	} else if users := m.dependentsLocked(id, true); len(users) > 0 {
		return fmt.Errorf("%w: turn off %s first", ErrRequiredBy, strings.Join(users, ", "))
	}
	e.Enabled = enabled
	e.DisabledReason = ""
	return m.reg.Put(e)
}

// requirementsMetLocked says whether every plugin in requires is installed and turned on.
func (m *Manager) requirementsMetLocked(requires []string) error {
	var missing []string
	for _, id := range requires {
		if have, ok := m.reg.Get(id); !ok || !have.Enabled {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrRequirementMissing, strings.Join(missing, ", "))
	}
	return nil
}

// dependentsLocked lists the installed plugins that require id (only the turned-on ones with onlyEnabled).
func (m *Manager) dependentsLocked(id string, onlyEnabled bool) []string {
	var out []string
	for _, e := range m.reg.List() {
		if slices.Contains(e.Requires, id) && (e.Enabled || !onlyEnabled) {
			out = append(out, e.ID)
		}
	}
	return out
}

// markDisabled turns a plugin off on the panel's own initiative and records why.
func (m *Manager) markDisabled(id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.reg.Get(id)
	if !ok {
		return ErrNotFound
	}
	e.Enabled = false
	e.DisabledReason = reason
	return m.reg.Put(e)
}

// Uninstall forgets the plugin first, then deletes its code and, on request, its data.
func (m *Manager) Uninstall(id string, removeData bool) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%w: invalid id %q", ErrNotFound, id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.reg.Get(id); !ok {
		return ErrNotFound
	}
	if users := m.dependentsLocked(id, false); len(users) > 0 {
		return fmt.Errorf("%w: remove %s first", ErrRequiredBy, strings.Join(users, ", "))
	}
	if h, ok := m.proc.(interface{ RunUninstallHook(context.Context, Entry) }); ok {
		e, _ := m.reg.Get(id)
		h.RunUninstallHook(context.Background(), e)
	}
	if m.proc != nil {
		if err := m.proc.StopPlugin(context.Background(), id); err != nil {
			return fmt.Errorf("cannot stop the plugin process, nothing was removed: %w", err)
		}
	}
	if err := m.reg.Delete(id); err != nil {
		return err
	}
	err := errors.Join(os.RemoveAll(filepath.Join(m.cfg.CodeDir, id)), os.RemoveAll(filepath.Join(m.cfg.CodeDir, id+previousSuffix)))
	if removeData {
		err = errors.Join(err, removePluginData(filepath.Join(m.cfg.DataDir, id), id))
	}
	logx.Infof("plugins: uninstalled %s (data removed: %v)", id, removeData)
	return err
}

// markInstallHookDone remembers that hooks.install succeeded for good.
func (m *Manager) markInstallHookDone(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.reg.Get(id)
	if !ok {
		return ErrNotFound
	}
	e.InstallHookDone = true
	return m.reg.Put(e)
}

func (m *Manager) sweepLocked() {
	now := m.cfg.Now()
	for token, s := range m.staged {
		if now.After(s.preview.ExpiresAt) {
			_ = os.RemoveAll(s.dir)
			delete(m.staged, token)
		}
	}
}

func (m *Manager) evictOldestLocked() {
	for len(m.staged) > MaxPendingPreviews {
		var oldest string
		for token, s := range m.staged {
			if oldest == "" || s.preview.ExpiresAt.Before(m.staged[oldest].preview.ExpiresAt) {
				oldest = token
			}
		}
		_ = os.RemoveAll(m.staged[oldest].dir)
		delete(m.staged, oldest)
	}
}

func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// AllowMasterManagement reports whether a master panel may manage plugins here.
func (m *Manager) AllowMasterManagement() bool { return m.reg.AllowMasterManagement() }

// SetAllowMasterManagement records the node owner's decision.
func (m *Manager) SetAllowMasterManagement(allow bool) error {
	return m.reg.SetAllowMasterManagement(allow)
}

// replaceConflict refuses a plugin that replaces a page another installed plugin already replaces.
func (m *Manager) replaceConflict(manifest *Manifest) error {
	for _, other := range m.reg.List() {
		if other.ID == manifest.ID {
			continue
		}
		for _, route := range manifest.UI.Replaces {
			if slices.Contains(other.UI.Replaces, route) {
				return fmt.Errorf("%w: %s already replaces %s", ErrReplaceConflict, other.ID, route)
			}
		}
	}
	return nil
}

// removePluginData deletes a plugin's data directory. Under DynamicUser that path is a link to the
// real state directory; the real one goes too, but only when it is named after the plugin.
func removePluginData(path, id string) error {
	if real, err := filepath.EvalSymlinks(path); err == nil && real != path && filepath.Base(real) == id {
		if err := os.RemoveAll(real); err != nil {
			return err
		}
	}
	return os.RemoveAll(path)
}
