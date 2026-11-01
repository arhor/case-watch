package credentials

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Prepare retains every existing key and leaves the old active ID unchanged.
// An existing inactive ID resumes an interrupted rotation using the persisted key.
func (k *Keyring) Prepare(id string) (*Keyring, error) {
	if k == nil || strings.TrimSpace(id) == "" || !utf8.ValidString(id) || id == k.active {
		return nil, ErrKeyring
	}
	n := &Keyring{active: k.active, keys: make(map[string][]byte)}
	for id, key := range k.keys {
		n.keys[id] = append([]byte(nil), key...)
	}
	if _, ok := n.keys[id]; !ok {
		key := make([]byte, 32)
		if _, e := rand.Read(key); e != nil {
			return nil, ErrKeyring
		}
		n.keys[id] = key
	}
	return n, nil
}
func (k *Keyring) Activate(id string) (*Keyring, error) {
	if k == nil {
		return nil, ErrKeyring
	}
	if _, ok := k.keys[id]; !ok {
		return nil, ErrKeyring
	}
	n := &Keyring{active: id, keys: k.keys}
	return n, nil
}

// Replace atomically persists a complete restricted keyring, then fsyncs its
// directory. The old file must already validate; no missing key is provisioned.
func (k *Keyring) Replace(path string) error {
	if _, e := Load(path); e != nil {
		return ErrKeyring
	}
	dir, e := directory(path)
	if e != nil {
		return ErrKeyring
	}
	defer dir.Close()
	doc := document{Version: 1, Active: k.active, Keys: map[string]string{}}
	for id, key := range k.keys {
		doc.Keys[id] = base64.StdEncoding.EncodeToString(key)
	}
	data, e := json.Marshal(doc)
	if e != nil {
		return ErrKeyring
	}
	defer clear(data)
	if _, e = Parse(data); e != nil {
		return ErrKeyring
	}
	random := make([]byte, 16)
	if _, e = rand.Read(random); e != nil {
		return ErrKeyring
	}
	name := ".keyring-" + hex.EncodeToString(random)
	f, e := dir.OpenFile(name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return ErrKeyring
	}
	defer dir.Remove(name)
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return ErrKeyring
	}
	// Revalidate the destination immediately before replacement, rejecting links.
	if _, e = Load(path); e != nil {
		return ErrKeyring
	}
	if e = dir.Rename(name, filepath.Base(path)); e != nil {
		return ErrKeyring
	}
	df, e := dir.Open(".")
	if e != nil {
		return ErrKeyring
	}
	defer df.Close()
	if df.Sync() != nil {
		return ErrKeyring
	}
	return nil
}
