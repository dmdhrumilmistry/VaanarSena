package main

import (
	"crypto/x509"
	"errors"
	"os"
)

func loadPool(file string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("VS_CA_FILE contains no PEM certificates")
	}
	return pool, nil
}
