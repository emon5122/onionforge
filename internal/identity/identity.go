// Package identity manages persistent Tor v3 onion service identities.
//
// Each service owns one directory below the data directory containing Tor's
// hs_ed25519_secret_key (and derived public key / hostname files). The secret
// key IS the onion address: losing it loses the address, so nothing in this
// package ever deletes or overwrites an existing secret key, and key material
// is never logged.
package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/edwards25519"
)

// File names used by Tor inside a HiddenServiceDir.
const (
	HostnameFile  = "hostname"
	PublicKeyFile = "hs_ed25519_public_key"
	SecretKeyFile = "hs_ed25519_secret_key"

	publicKeyHeader = "== ed25519v1-public: type0 ==\x00\x00\x00"
	secretKeyHeader = "== ed25519v1-secret: type0 ==\x00\x00\x00"
)

var onionBase32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// ErrNotFound is returned when a service has no identity yet.
var ErrNotFound = errors.New("identity not found")

// Identity is a loaded onion service identity. It intentionally does not
// expose the secret key.
type Identity struct {
	Name     string
	Dir      string
	Hostname string // e.g. "abc...xyz.onion"
}

// Keypair is freshly generated key material in Tor's on-disk format.
type Keypair struct {
	secret []byte // 96-byte hs_ed25519_secret_key contents
	public []byte // 64-byte hs_ed25519_public_key contents
	// Hostname derived from the public key.
	Hostname string
}

// String never reveals key material.
func (k *Keypair) String() string { return "Keypair{" + k.Hostname + "}" }

// Dir returns the identity directory for a service.
func Dir(dataDir, name string) string { return filepath.Join(dataDir, name) }

// OnionAddress encodes an ed25519 public key as a v3 onion hostname
// (rend-spec-v3 §6).
func OnionAddress(pub []byte) string {
	const version = 0x03
	h := sha3.New256()
	h.Write([]byte(".onion checksum"))
	h.Write(pub)
	h.Write([]byte{version})
	sum := h.Sum(nil)
	buf := make([]byte, 0, 35)
	buf = append(buf, pub...)
	buf = append(buf, sum[:2]...)
	buf = append(buf, version)
	return onionBase32.EncodeToString(buf) + ".onion"
}

// publicFromExpanded derives the public key from Tor's 64-byte expanded
// secret key. Tor uses the first 32 bytes as the scalar as-is.
func publicFromExpanded(expanded []byte) ([]byte, error) {
	wide := make([]byte, 64)
	copy(wide, expanded[:32])
	s, err := edwards25519.NewScalar().SetUniformBytes(wide)
	if err != nil {
		return nil, err
	}
	return new(edwards25519.Point).ScalarBaseMult(s).Bytes(), nil
}

// ParseSecretKey validates hs_ed25519_secret_key contents and returns the
// corresponding keypair.
func ParseSecretKey(data []byte) (*Keypair, error) {
	expanded, ok := bytes.CutPrefix(data, []byte(secretKeyHeader))
	if !ok || len(expanded) != 64 {
		return nil, errors.New("malformed hs_ed25519_secret_key")
	}
	pub, err := publicFromExpanded(expanded)
	if err != nil {
		return nil, fmt.Errorf("malformed hs_ed25519_secret_key: %w", err)
	}
	return &Keypair{
		secret:   bytes.Clone(data),
		public:   append([]byte(publicKeyHeader), pub...),
		Hostname: OnionAddress(pub),
	}, nil
}

// GenerateRandom creates a new random identity the same way Tor does: an
// ed25519 seed expanded with SHA-512 and clamped.
func GenerateRandom() (*Keypair, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	defer clear(seed)
	h := sha512.Sum512(seed)
	defer clear(h[:])
	h[0] &= 248
	h[31] &= 63
	h[31] |= 64
	kp, err := ParseSecretKey(append([]byte(secretKeyHeader), h[:]...))
	if err != nil {
		return nil, err
	}
	// Cross-check the derivation against the standard library.
	std := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !bytes.Equal(std, kp.public[len(publicKeyHeader):]) {
		return nil, errors.New("internal error: public key derivation mismatch")
	}
	return kp, nil
}

// Load reads the identity in dir. It returns ErrNotFound when the directory
// holds no identity at all, and an error when it holds a partial or corrupt
// one (which must never be silently replaced).
func Load(name, dir string) (*Identity, error) {
	secret, err := os.ReadFile(filepath.Join(dir, SecretKeyFile))
	if errors.Is(err, fs.ErrNotExist) {
		leftovers, lerr := identityLeftovers(dir)
		if lerr != nil {
			return nil, lerr
		}
		if len(leftovers) > 0 {
			return nil, fmt.Errorf("identity directory %s contains %s but no %s: refusing to create a new identity over it (restore the secret key from backup, or move the directory away to start fresh)",
				dir, strings.Join(leftovers, ", "), SecretKeyFile)
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Join(dir, SecretKeyFile), err)
	}
	kp, err := ParseSecretKey(secret)
	clear(secret)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if pub, err := os.ReadFile(filepath.Join(dir, PublicKeyFile)); err == nil && !bytes.Equal(pub, kp.public) {
		return nil, fmt.Errorf("%s: %s does not match %s; the identity is corrupt (the secret key is authoritative: delete %s to let Tor regenerate it)",
			dir, PublicKeyFile, SecretKeyFile, PublicKeyFile)
	}
	return &Identity{Name: name, Dir: dir, Hostname: kp.Hostname}, nil
}

// identityLeftovers lists files that suggest an identity once existed in dir.
func identityLeftovers(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		switch e.Name() {
		case HostnameFile, PublicKeyFile:
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Write persists a new keypair into dir. It refuses to touch a directory
// that already contains a secret key. Files are written into a temporary
// sibling directory and renamed into place so a crash never leaves a
// half-written identity.
func Write(dir string, kp *Keypair) error {
	if _, err := os.Stat(filepath.Join(dir, SecretKeyFile)); err == nil {
		return fmt.Errorf("refusing to overwrite existing identity in %s", dir)
	}
	parent := filepath.Dir(dir)
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".new-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o700); err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(tmp, SecretKeyFile), kp.secret); err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(tmp, PublicKeyFile), kp.public); err != nil {
		return err
	}
	// A pre-existing empty directory (e.g. created by hand) is replaced. If it
	// cannot be removed (a mount point), write into it directly instead.
	if _, err := os.Stat(dir); err == nil {
		if err := os.Remove(dir); err != nil {
			if err := writeFileSync(filepath.Join(dir, SecretKeyFile), kp.secret); err != nil {
				return fmt.Errorf("installing identity into %s: %w", dir, err)
			}
			if err := writeFileSync(filepath.Join(dir, PublicKeyFile), kp.public); err != nil {
				return fmt.Errorf("installing identity into %s: %w", dir, err)
			}
			return syncDir(dir)
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("installing identity into %s: %w", dir, err)
	}
	return syncDir(parent)
}

func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// ReadHostname reads the hostname file Tor wrote for dir.
func ReadHostname(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, HostnameFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// HasPrefix reports whether an onion hostname starts with prefix.
func HasPrefix(hostname, prefix string) bool {
	return strings.HasPrefix(hostname, prefix)
}
