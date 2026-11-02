package network

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// identity loads or creates a persistent owner-only TLS identity.
func identity(dir string) (tls.Certificate, string, error) {
	path := filepath.Join(dir, "identity.pem")
	data, err := readPrivate(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := privateDir(dir); err != nil {
			return tls.Certificate{}, "", err
		}
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			return tls.Certificate{}, "", err
		}
		template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "rmt host"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, public, private)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		key, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		data = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})...)
		f, err := os.CreateTemp(dir, ".identity-*")
		if err != nil {
			return tls.Certificate{}, "", err
		}
		defer os.Remove(f.Name())
		_, err = f.Write(data)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return tls.Certificate{}, "", err
		}
		if err = os.Link(f.Name(), path); errors.Is(err, os.ErrExist) {
			data, err = readPrivate(path)
		}
		if err != nil {
			return tls.Certificate{}, "", err
		}
	} else if err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.X509KeyPair(data, data)
	if err != nil || len(cert.Certificate) == 0 {
		return tls.Certificate{}, "", errors.New("invalid host TLS identity")
	}
	sum := sha256.Sum256(cert.Certificate[0])
	return cert, hex.EncodeToString(sum[:]), nil
}
