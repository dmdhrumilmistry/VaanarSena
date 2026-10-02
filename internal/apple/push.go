package apple

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const apnsHost = "https://api.push.apple.com"

// Pusher sends MDM wake-up notifications through APNs using the MDM push
// certificate issued by identity.apple.com.
type Pusher struct {
	client *http.Client
	Topic  string
	Expiry time.Time
	host   string
}

var oidUID = asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}

// NewPusher loads the push certificate. topic may be empty to read it from the
// certificate's UID attribute.
func NewPusher(certFile, keyFile, topic string) (*Pusher, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	return NewPusherPEM(certPEM, keyPEM, topic)
}

// NewPusherPEM is NewPusher with the certificate and key in memory.
func NewPusherPEM(certPEM, keyPEM []byte, topic string) (*Pusher, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load APNs certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if topic == "" {
		for _, n := range leaf.Subject.Names {
			if n.Type.Equal(oidUID) {
				topic, _ = n.Value.(string)
			}
		}
	}
	if topic == "" {
		return nil, errors.New("APNs topic not found in certificate; set VS_APNS_TOPIC")
	}
	tr := &http.Transport{
		TLSClientConfig:   &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
		IdleConnTimeout:   5 * time.Minute,
	}
	return &Pusher{
		client: &http.Client{Transport: tr, Timeout: 15 * time.Second},
		Topic:  topic,
		Expiry: leaf.NotAfter,
		host:   apnsHost,
	}, nil
}

// Push wakes one device. tokenHex is the hex device token from TokenUpdate.
func (p *Pusher) Push(ctx context.Context, tokenHex, pushMagic string) error {
	body, _ := json.Marshal(map[string]string{"mdm": pushMagic})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/3/device/"+tokenHex, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("apns-topic", p.Topic)
	req.Header.Set("apns-push-type", "mdm")
	req.Header.Set("apns-priority", "10")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("APNs %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	return nil
}
