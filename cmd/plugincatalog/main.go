// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Command plugincatalog publishes the signed plugin catalog: it makes the signing key, adds plugins to
// index.json, signs it and checks a signature, all with the same code the panel verifies with.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

const usage = `usage: plugincatalog <command> [flags]

  keygen -out <file>                       make the signing key (kept secret, never overwritten); prints the public key
  pubkey -key <file>                       print the public key of a signing key
  add    -index <file> -repo <url> -commit <sha> -dir <plugin dir> [-summary ..] [-verified-by ..] [-verified-at YYYY-MM-DD] [-notes ..]
                                           add or replace a plugin in index.json (fills the rest from its lucx-plugin.json)
  remove -index <file> -id <plugin id>     take a plugin out of index.json and raise the sequence
  sign   -key <file> -index <file>         validate index.json and write index.json.sig next to it
  verify -index <file> -sig <file> -pub <base64 key>[,<key>...]
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "keygen":
		err = keygen(args[1:], stdout)
	case "pubkey":
		err = pubkey(args[1:], stdout)
	case "add":
		err = add(args[1:])
	case "remove":
		err = remove(args[1:])
	case "sign":
		err = sign(args[1:])
	case "verify":
		err = verify(args[1:], stdout)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "plugincatalog:", err)
		return 1
	}
	return 0
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func keygen(args []string, stdout io.Writer) error {
	fs := newFlags("keygen")
	out := fs.String("out", "", "where to write the private key")
	if err := fs.Parse(args); err != nil || *out == "" {
		return errors.New("keygen needs -out <file>")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("cannot create %s (an existing key is never overwritten): %w", *out, err)
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv) + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(pub))
	return nil
}

func readKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is not a catalog signing key", path)
	}
	return ed25519.PrivateKey(key), nil
}

func pubkey(args []string, stdout io.Writer) error {
	fs := newFlags("pubkey")
	keyPath := fs.String("key", "", "signing key file")
	if err := fs.Parse(args); err != nil || *keyPath == "" {
		return errors.New("pubkey needs -key <file>")
	}
	key, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	return nil
}

func sign(args []string) error {
	fs := newFlags("sign")
	keyPath := fs.String("key", "", "signing key file")
	index := fs.String("index", "", "index.json")
	if err := fs.Parse(args); err != nil || *keyPath == "" || *index == "" {
		return errors.New("sign needs -key <file> -index <file>")
	}
	key, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*index)
	if err != nil {
		return err
	}
	if _, err := plugins.ParseCatalogIndex(raw); err != nil {
		return fmt.Errorf("refusing to sign an index the panel would refuse: %w", err)
	}
	return writeFile(*index+".sig", []byte(plugins.SignCatalog(key, raw)+"\n"))
}

func verify(args []string, stdout io.Writer) error {
	fs := newFlags("verify")
	index := fs.String("index", "", "index.json")
	sig := fs.String("sig", "", "index.json.sig")
	pub := fs.String("pub", "", "base64 public key(s), comma separated")
	if err := fs.Parse(args); err != nil || *index == "" || *sig == "" || *pub == "" {
		return errors.New("verify needs -index <file> -sig <file> -pub <key>")
	}
	keys, err := plugins.ParseCatalogKeys(strings.Split(*pub, ","))
	if err != nil {
		return err
	}
	rawIndex, err := os.ReadFile(*index)
	if err != nil {
		return err
	}
	rawSig, err := os.ReadFile(*sig)
	if err != nil {
		return err
	}
	if err := plugins.VerifyCatalog(rawIndex, string(rawSig), keys); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "signature ok")
	return nil
}

func add(args []string) error {
	fs := newFlags("add")
	index := fs.String("index", "", "index.json to create or update")
	repo := fs.String("repo", "", "https://github.com/<owner>/<repo>")
	commit := fs.String("commit", "", "full commit SHA that was reviewed (with -track it is the last reviewed one, which a panel without -track support still installs)")
	track := fs.Bool("track", false, "vouch for the repository, not a commit: installs and updates follow its default branch")
	dir := fs.String("dir", "", "the plugin checked out at that commit")
	summary := fs.String("summary", "", "one line shown on the card")
	by := fs.String("verified-by", "", "who reviewed it")
	at := fs.String("verified-at", "", "review date, YYYY-MM-DD (default today)")
	notes := fs.String("notes", "", "short review note")
	if err := fs.Parse(args); err != nil || *index == "" || *repo == "" || (*commit == "" && !*track) || *dir == "" {
		return errors.New("add needs -index, -repo, -dir and -commit or -track")
	}
	rawManifest, err := os.ReadFile(filepath.Join(*dir, plugins.ManifestFile))
	if err != nil {
		return err
	}
	m, err := plugins.ParseManifest(rawManifest)
	if err != nil {
		return fmt.Errorf("the plugin's manifest is invalid: %w", err)
	}
	sum := sha256.Sum256(rawManifest)
	entry := plugins.CatalogEntry{
		ID: m.ID, Name: m.Name, Summary: *summary, Repo: *repo, Commit: *commit, Track: *track, Version: m.Version,
		ManifestSHA256: hex.EncodeToString(sum[:]), Type: m.Type, Scope: m.Scope, License: m.License, Author: m.Author,
		Permissions: normalized(m.Permissions),
	}
	if *track && *commit == "" {
		entry.ManifestSHA256 = "" // nothing is pinned: the manifest changes with every version
	}
	if entry.Summary == "" {
		entry.Summary = truncate(m.Description, 300)
	}
	if len(m.UI.Menu) > 0 {
		entry.Icon = m.UI.Menu[0].Icon
	}
	if *by != "" {
		date := *at
		if date == "" {
			date = time.Now().UTC().Format("2006-01-02")
		}
		entry.Verified = &plugins.CatalogVerified{By: *by, At: date, Notes: *notes}
	}

	idx := plugins.CatalogIndex{Schema: 1}
	if raw, err := os.ReadFile(*index); err == nil {
		parsed, err := plugins.ParseCatalogIndex(raw)
		if err != nil {
			return fmt.Errorf("%s is not a valid catalog: %w", *index, err)
		}
		idx = *parsed
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	idx.Plugins = slices.DeleteFunc(idx.Plugins, func(e plugins.CatalogEntry) bool { return e.ID == entry.ID })
	idx.Plugins = append(idx.Plugins, entry)
	slices.SortFunc(idx.Plugins, func(a, b plugins.CatalogEntry) int { return strings.Compare(a.ID, b.ID) })
	idx.Sequence++
	idx.GeneratedAt = time.Now().UTC().Truncate(time.Second)

	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if _, err := plugins.ParseCatalogIndex(out); err != nil {
		return fmt.Errorf("the entry would make the catalog invalid: %w", err)
	}
	return writeFile(*index, append(out, '\n'))
}

func normalized(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plugincatalog-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func remove(args []string) error {
	fs := newFlags("remove")
	index := fs.String("index", "", "index.json to change")
	id := fs.String("id", "", "the plugin to take out")
	if err := fs.Parse(args); err != nil || *index == "" || *id == "" {
		return errors.New("remove needs -index and -id")
	}
	raw, err := os.ReadFile(*index)
	if err != nil {
		return err
	}
	idx, err := plugins.ParseCatalogIndex(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid catalog: %w", *index, err)
	}
	before := len(idx.Plugins)
	idx.Plugins = slices.DeleteFunc(idx.Plugins, func(e plugins.CatalogEntry) bool { return e.ID == *id })
	if len(idx.Plugins) == before {
		return fmt.Errorf("%s is not in the catalog", *id)
	}
	idx.Sequence++
	idx.GeneratedAt = time.Now().UTC().Truncate(time.Second)
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if _, err := plugins.ParseCatalogIndex(out); err != nil {
		return fmt.Errorf("the result would be an invalid catalog: %w", err)
	}
	return writeFile(*index, append(out, '\n'))
}
