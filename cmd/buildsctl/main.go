// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Command buildsctl keeps the signed builds.json: it makes the signing key, describes a finished
// build, adds it to the list, signs the list and checks a signature, with the same code the
// installer verifies with.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

const usage = `usage: buildsctl <command> [flags]

  keygen -out <file>                 make the signing key (never overwritten); prints the public key
  pubkey (-key <file> | -key-env <VAR>)
  meta   -upstream <tag> -arch <amd64|arm64> -release <tag> -original-binary-sha256 <h> -original-archive-sha256 <h>
         -build-archive-sha256 <h> -binary-sha256 <h> -out <file>      describe one finished build
  add    -index <file> -meta <file>  add (or replace) a build; creates the index when missing, raises the sequence
  sign   (-key <file> | -key-env <VAR>) -index <file>    validate the index and write <index>.sig
  verify -index <file> -sig <file> -pub <base64 key>[,<key>...]

  host-set    -index <file> -version host-<n> -amd64-sha256 <h> -arm64-sha256 <h>
              make (or move forward) host.json, the signed record of the current plugin host build
  host-sign   (-key <file> | -key-env <VAR>) -index <file>    validate host.json and write <index>.sig
  host-verify -index <file> -sig <file> -pub <base64 key>[,<key>...]
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

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
	case "meta":
		err = meta(args[1:])
	case "add":
		err = add(args[1:])
	case "sign":
		err = sign(args[1:])
	case "verify":
		err = verify(args[1:], stdout)
	case "host-set":
		err = hostSet(args[1:])
	case "host-sign":
		err = hostSign(args[1:])
	case "host-verify":
		err = hostVerify(args[1:], stdout)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "buildsctl:", err)
		return 1
	}
	return 0
}

func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func keygen(args []string, stdout io.Writer) error {
	fs := flags("keygen")
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

// loadKey reads the private key from a file or from an environment variable (for CI secrets).
func loadKey(file, env string) (ed25519.PrivateKey, error) {
	var text string
	switch {
	case file != "":
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		text = string(raw)
	case env != "":
		text = os.Getenv(env)
		if text == "" {
			return nil, fmt.Errorf("environment variable %s is empty or not set", env)
		}
	default:
		return nil, errors.New("give -key <file> or -key-env <VAR>")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(text))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("the signing key is not a base64 ed25519 private key")
	}
	return ed25519.PrivateKey(key), nil
}

func pubkey(args []string, stdout io.Writer) error {
	fs := flags("pubkey")
	file := fs.String("key", "", "signing key file")
	env := fs.String("key-env", "", "environment variable holding the signing key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	key, err := loadKey(*file, *env)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	return nil
}

func meta(args []string) error {
	fs := flags("meta")
	b := builds.Build{}
	fs.StringVar(&b.Upstream, "upstream", "", "upstream release tag")
	fs.StringVar(&b.Arch, "arch", "", "amd64 or arm64")
	fs.StringVar(&b.Release, "release", "", "our release tag, <upstream>-plugins.<n>")
	fs.StringVar(&b.OriginalBinarySha256, "original-binary-sha256", "", "sha256 of the upstream x-ui binary")
	fs.StringVar(&b.OriginalArchive.SHA256, "original-archive-sha256", "", "sha256 of the upstream tarball")
	fs.StringVar(&b.Build.SHA256, "build-archive-sha256", "", "sha256 of our tarball")
	fs.StringVar(&b.BinarySha256, "binary-sha256", "", "sha256 of our x-ui binary")
	out := fs.String("out", "", "where to write the description")
	if err := fs.Parse(args); err != nil || *out == "" {
		return errors.New("meta needs its flags and -out <file>; see usage")
	}
	b.OriginalArchive.URL = "https://github.com/" + builds.UpstreamRepo + "/releases/download/" + b.Upstream + "/x-ui-linux-" + b.Arch + ".tar.gz"
	b.Build.URL = "https://github.com/" + builds.OwnRepo + "/releases/download/" + b.Release + "/x-ui-plugins-linux-" + b.Arch + ".tar.gz"
	idx := builds.Index{Schema: builds.SchemaVersion, Sequence: 1, Builds: []builds.Build{b}}
	if err := idx.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, append(raw, '\n'), 0o644)
}

func add(args []string) error {
	fs := flags("add")
	path := fs.String("index", "", "builds.json to create or update")
	metaPath := fs.String("meta", "", "build description made by the meta command")
	if err := fs.Parse(args); err != nil || *path == "" || *metaPath == "" {
		return errors.New("add needs -index <file> and -meta <file>")
	}
	var b builds.Build
	raw, err := os.ReadFile(*metaPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return fmt.Errorf("%s: %w", *metaPath, err)
	}
	idx := &builds.Index{Schema: builds.SchemaVersion}
	if current, err := os.ReadFile(*path); err == nil {
		if idx, err = builds.Parse(current); err != nil {
			return fmt.Errorf("the existing %s is invalid: %w", *path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := idx.Add(b); err != nil {
		return err
	}
	idx.GeneratedAt = time.Now().UTC().Truncate(time.Second)
	out, err := builds.Marshal(idx)
	if err != nil {
		return err
	}
	return os.WriteFile(*path, out, 0o644)
}

func sign(args []string) error {
	fs := flags("sign")
	file := fs.String("key", "", "signing key file")
	env := fs.String("key-env", "", "environment variable holding the signing key")
	path := fs.String("index", "", "builds.json")
	if err := fs.Parse(args); err != nil || *path == "" {
		return errors.New("sign needs a key and -index <file>")
	}
	key, err := loadKey(*file, *env)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	if _, err := builds.Parse(data); err != nil {
		return fmt.Errorf("refusing to sign an index clients would refuse: %w", err)
	}
	return os.WriteFile(*path+".sig", []byte(builds.Sign(key, data)+"\n"), 0o644)
}

func verify(args []string, stdout io.Writer) error {
	fs := flags("verify")
	path := fs.String("index", "", "builds.json")
	sigPath := fs.String("sig", "", "builds.json.sig")
	pub := fs.String("pub", "", "base64 public key(s), comma separated")
	if err := fs.Parse(args); err != nil || *path == "" || *sigPath == "" || *pub == "" {
		return errors.New("verify needs -index, -sig and -pub")
	}
	keys, err := builds.ParseKeys(strings.Split(*pub, ","))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(*sigPath)
	if err != nil {
		return err
	}
	if err := builds.Verify(data, string(sig), keys); err != nil {
		return err
	}
	if _, err := builds.Parse(data); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "signature ok")
	return nil
}

func hostSet(args []string) error {
	fs := flags("host-set")
	path := fs.String("index", "", "host.json to create or update")
	version := fs.String("version", "", "host-<n>")
	amd64 := fs.String("amd64-sha256", "", "sha256 of the amd64 binary")
	arm64 := fs.String("arm64-sha256", "", "sha256 of the arm64 binary")
	if err := fs.Parse(args); err != nil || *path == "" || *version == "" {
		return errors.New("host-set needs -index, -version and both hashes")
	}
	doc := &builds.HostIndex{Schema: builds.HostSchemaVersion, Sequence: 1}
	if current, err := os.ReadFile(*path); err == nil {
		if doc, err = builds.ParseHost(current); err != nil {
			return fmt.Errorf("the existing %s is invalid: %w", *path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := doc.Set(*version, map[string]string{"amd64": *amd64, "arm64": *arm64}); err != nil {
		return err
	}
	doc.GeneratedAt = time.Now().UTC().Truncate(time.Second)
	out, err := builds.MarshalHost(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(*path, out, 0o644)
}

func hostSign(args []string) error {
	fs := flags("host-sign")
	file := fs.String("key", "", "signing key file")
	env := fs.String("key-env", "", "environment variable holding the signing key")
	path := fs.String("index", "", "host.json")
	if err := fs.Parse(args); err != nil || *path == "" {
		return errors.New("host-sign needs a key and -index <file>")
	}
	key, err := loadKey(*file, *env)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	if _, err := builds.ParseHost(data); err != nil {
		return fmt.Errorf("refusing to sign a document clients would refuse: %w", err)
	}
	return os.WriteFile(*path+".sig", []byte(builds.SignHost(key, data)+"\n"), 0o644)
}

func hostVerify(args []string, stdout io.Writer) error {
	fs := flags("host-verify")
	path := fs.String("index", "", "host.json")
	sigPath := fs.String("sig", "", "host.json.sig")
	pub := fs.String("pub", "", "base64 public key(s), comma separated")
	if err := fs.Parse(args); err != nil || *path == "" || *sigPath == "" || *pub == "" {
		return errors.New("host-verify needs -index, -sig and -pub")
	}
	keys, err := builds.ParseKeys(strings.Split(*pub, ","))
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(*sigPath)
	if err != nil {
		return err
	}
	if err := builds.VerifyHost(data, string(sig), keys); err != nil {
		return err
	}
	if _, err := builds.ParseHost(data); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "signature ok")
	return nil
}
