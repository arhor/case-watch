package credentials

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/application"
)

func ringJSON() string {
	return `{"version":1,"active_key_id":"first","keys":{"first":"` + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)) + `"}}`
}
func testRing(t *testing.T) *Keyring {
	t.Helper()
	k, err := Parse([]byte(ringJSON()))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealShouldRecoverCredentialsAndUseFreshNoncesWhenRepeated(t *testing.T) {
	// Given
	k := testRing(t)
	plain := application.Credentials{Number: strings.Repeat("synthetic-number", 2), Password: strings.Repeat("synthetic-password", 2)}
	// When
	first, err := k.Seal("record-1", plain)
	second, err2 := k.Seal("record-1", plain)
	recovered, err3 := k.Open(first)
	// Then
	if err != nil || err2 != nil || err3 != nil || recovered != plain || bytes.Equal(first.Payload, second.Payload) || bytes.Equal(first.Payload[:12], second.Payload[:12]) {
		t.Fatal("roundtrip or nonce separation failed")
	}
	if bytes.Contains(first.Payload, []byte(plain.Number)) || bytes.Contains(first.Payload, []byte(plain.Password)) {
		t.Fatal("ciphertext exposed plaintext")
	}
}

func TestOpenShouldRejectCiphertextWhenAuthenticationContextChanges(t *testing.T) {
	for _, mutation := range []string{"record", "version", "unknown-key", "key-id", "wrong-key", "tamper", "truncate", "empty"} {
		t.Run(mutation, func(t *testing.T) {
			// Given
			k := testRing(t)
			original := application.Credentials{Number: "synthetic", Password: "synthetic"}
			sealed, err := k.Seal("record", original)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "record":
				sealed.ID = "other"
			case "version":
				sealed.FormatVersion = 2
			case "unknown-key":
				sealed.KeyID = "absent"
			case "key-id":
				k.keys["other"] = k.keys["first"]
				sealed.KeyID = "other"
			case "wrong-key":
				k.keys["first"] = bytes.Repeat([]byte{8}, 32)
			case "tamper":
				sealed.Payload[len(sealed.Payload)-1] ^= 1
			case "truncate":
				sealed.Payload = sealed.Payload[:10]
			case "empty":
				sealed.Payload = nil
			}
			// When
			got, err := k.Open(sealed)
			// Then
			if err != ErrCredentials || got != (application.Credentials{}) {
				t.Fatal("invalid record returned credentials or unsafe error")
			}
		})
	}
}

func TestParseShouldRejectKeyringWhenDocumentInvalid(t *testing.T) {
	valid := ringJSON()
	for _, input := range []string{"", "null", "[]", valid + ` {}`, strings.Replace(valid, `"version":1`, `"version":2`, 1), strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(valid, `"keys":{`, `"keys":{"first":"bad",`, 1), strings.ReplaceAll(valid, "first", ""), strings.Replace(valid, `"active_key_id":"first"`, `"active_key_id":"absent"`, 1), strings.Replace(valid, `"keys":{`, `"extra":true,"keys":{`, 1), strings.Replace(valid, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), "bad", 1), strings.Replace(valid, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), base64.StdEncoding.EncodeToString([]byte{1}), 1)} {
		t.Run("invalid", func(t *testing.T) {
			// Given
			data := []byte(input)
			// When
			k, err := Parse(data)
			// Then
			if err != ErrKeyring || k != nil {
				t.Fatal("invalid keyring accepted")
			}
		})
	}
}

func TestProvisionShouldCreateLoadableKeyAndRefuseOverwriteWhenExplicitlyRequested(t *testing.T) {
	// Given
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keyring.json")
	// When
	err := Provision(path, "initial")
	k, loadErr := Load(path)
	before, readErr := os.ReadFile(path)
	again := Provision(path, "replacement")
	after, readErr2 := os.ReadFile(path)
	// Then
	if err != nil || loadErr != nil || readErr != nil || readErr2 != nil || k.active != "initial" || again != ErrKeyring || !bytes.Equal(before, after) {
		t.Fatal("explicit provisioning or overwrite guard failed")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("key permissions invalid")
	}
}

func TestLoadShouldRejectFileWhenMissingOrInsecure(t *testing.T) {
	for _, kind := range []string{"missing", "file-permissions", "directory-permissions", "symlink", "directory", "directory-symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			// Given
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			path := filepath.Join(dir, "keyring")
			if kind != "missing" {
				if err := os.WriteFile(path, []byte(ringJSON()), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "file-permissions":
				os.Chmod(path, 0644)
			case "directory-permissions":
				os.Chmod(dir, 0755)
			case "symlink":
				os.Rename(path, path+".target")
				os.Symlink(path+".target", path)
			case "directory":
				os.Remove(path)
				os.Mkdir(path, 0600)
			case "directory-symlink":
				link := dir + "-link"
				os.Symlink(dir, link)
				defer os.Remove(link)
				path = filepath.Join(link, "keyring")
			case "fifo":
				os.Remove(path)
				if err := makeFIFO(path); err != nil {
					t.Fatal(err)
				}
			}
			// When
			k, err := Load(path)
			// Then
			if err != ErrKeyring || k != nil {
				t.Fatal("unsafe key file accepted")
			}
		})
	}
}
