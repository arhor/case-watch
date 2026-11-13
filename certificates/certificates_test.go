package certificates

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"testing"
)

func TestCertumDVTLSG2R39CA(t *testing.T) {
	data, err := os.ReadFile("certum-dv-tls-g2-r39-ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatal("invalid portal issuer PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	if certificate.Subject.CommonName != "Certum DV TLS G2 R39 CA" ||
		!certificate.IsCA ||
		hex.EncodeToString(fingerprint[:]) != "83c0a5a76844c840dfaf820ffd02adf6573a26823ef6af758a3384a0ac044083" {
		t.Fatal("unexpected portal issuer certificate")
	}
}
