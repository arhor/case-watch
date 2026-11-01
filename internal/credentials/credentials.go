// Package credentials implements ADR 0006's ciphertext-only credential boundary.
package credentials

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/arhor/case-watch/internal/application"
)

var (
	ErrKeyring     = errors.New("credential keyring unavailable or invalid")
	ErrCredentials = errors.New("credential encryption or authentication failed")
)

// Keyring holds validated keys in memory. No key material is exported.
type Keyring struct {
	active string
	keys   map[string][]byte
}
type document struct {
	Version int               `json:"version"`
	Active  string            `json:"active_key_id"`
	Keys    map[string]string `json:"keys"`
}

// unique rejects duplicate object members at every level, including key IDs.
func unique(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				name, ok := k.(string)
				if !ok || seen[name] {
					return ErrKeyring
				}
				seen[name] = true
				if e = unique(d); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e := unique(d); e != nil {
					return e
				}
			}
		default:
			return ErrKeyring
		}
		_, err = d.Token()
	}
	return err
}

// Parse accepts exactly one version-1 JSON document with no unknown or duplicate fields.
func Parse(data []byte) (*Keyring, error) {
	if !utf8.Valid(data) || len(data) > 1<<20 {
		return nil, ErrKeyring
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if unique(d) != nil {
		return nil, ErrKeyring
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrKeyring
	}
	var doc document
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&doc) != nil || doc.Version != 1 || strings.TrimSpace(doc.Active) == "" || len(doc.Keys) == 0 {
		return nil, ErrKeyring
	}
	k := &Keyring{active: doc.Active, keys: make(map[string][]byte)}
	for id, encoded := range doc.Keys {
		if strings.TrimSpace(id) == "" || strings.ContainsAny(encoded, "\r\n") {
			return nil, ErrKeyring
		}
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, ErrKeyring
		}
		k.keys[id] = key
	}
	if _, ok := k.keys[k.active]; !ok {
		return nil, ErrKeyring
	}
	return k, nil
}

func directory(path string) (*os.Root, error) {
	// NOFOLLOW prevents a symlink final directory. Restricted permissions also
	// prevent other accounts from replacing entries during file validation.
	fd, err := syscall.Open(filepath.Dir(path), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrKeyring
	}
	f := os.NewFile(uintptr(fd), "keyring directory")
	info, err := f.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, ErrKeyring
	}
	defer f.Close()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, ErrKeyring
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		return nil, ErrKeyring
	}
	return root, nil
}

// Load requires a non-symlink regular mode-0600 file in a restricted directory.
// Ancestors must be trusted operator-controlled paths. Errors never contain paths or key data.
func Load(path string) (*Keyring, error) {
	dir, err := directory(path)
	if err != nil {
		return nil, ErrKeyring
	}
	defer dir.Close()
	f, err := dir.OpenFile(filepath.Base(path), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrKeyring
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, ErrKeyring
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrKeyring
	}
	return Parse(data)
}

// Provision creates one random key under id. It never overwrites an existing
// entry, creates directories, or prints key material. Caller selects a separate protected path.
func Provision(path, id string) error {
	if strings.TrimSpace(id) == "" || !utf8.ValidString(id) {
		return ErrKeyring
	}
	dir, err := directory(path)
	if err != nil {
		return ErrKeyring
	}
	defer dir.Close()
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return ErrKeyring
	}
	data, err := json.Marshal(document{Version: 1, Active: id, Keys: map[string]string{id: base64.StdEncoding.EncodeToString(key)}})
	if err != nil {
		return ErrKeyring
	}
	f, err := dir.OpenFile(filepath.Base(path), syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return ErrKeyring
	}
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrKeyring
	}
	directoryFile, syncErr := dir.Open(".")
	if syncErr != nil {
		return ErrKeyring
	}
	defer directoryFile.Close()
	if directoryFile.Sync() != nil {
		return ErrKeyring
	}
	return nil
}

func aad(id application.EnrollmentID, version int, keyID string) []byte {
	// JSON array encodes field boundaries and types unambiguously.
	data, _ := json.Marshal([]any{"case-watch/portal-credentials", version, keyID, string(id)})
	return data
}
func (k *Keyring) aead(id string) (cipher.AEAD, error) {
	if k == nil {
		return nil, ErrCredentials
	}
	key, ok := k.keys[id]
	if !ok {
		return nil, ErrCredentials
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrCredentials
	}
	a, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, ErrCredentials
	}
	return a, nil
}

// Seal encrypts number and password together with fresh prefixed random nonce.
// Use fewer than 2^32 encryptions per key. The returned ID must remain immutable.
func (k *Keyring) Seal(id application.EnrollmentID, plaintext application.Credentials) (application.SealedCredentials, error) {
	if k == nil || strings.TrimSpace(string(id)) == "" || !utf8.ValidString(string(id)) || !utf8.ValidString(plaintext.Number) || !utf8.ValidString(plaintext.Password) {
		return application.SealedCredentials{}, ErrCredentials
	}
	a, err := k.aead(k.active)
	if err != nil {
		return application.SealedCredentials{}, ErrCredentials
	}
	data, err := json.Marshal(plaintext)
	if err != nil {
		return application.SealedCredentials{}, ErrCredentials
	}
	defer clear(data)
	return application.SealedCredentials{ID: id, FormatVersion: 1, KeyID: k.active, Payload: a.Seal(nil, nil, data, aad(id, 1, k.active))}, nil
}

// Open reconstructs AAD from the immutable destination record ID and its metadata.
// Storage must supply the destination ID, never an ID copied from another payload.
func (k *Keyring) Open(record application.SealedCredentials) (application.Credentials, error) {
	if record.FormatVersion != 1 || strings.TrimSpace(string(record.ID)) == "" || !utf8.ValidString(string(record.ID)) {
		return application.Credentials{}, ErrCredentials
	}
	a, err := k.aead(record.KeyID)
	if err != nil {
		return application.Credentials{}, ErrCredentials
	}
	data, err := a.Open(nil, nil, record.Payload, aad(record.ID, record.FormatVersion, record.KeyID))
	if err != nil {
		return application.Credentials{}, ErrCredentials
	}
	defer clear(data)
	var result application.Credentials
	if json.Unmarshal(data, &result) != nil {
		return application.Credentials{}, ErrCredentials
	}
	return result, nil
}
