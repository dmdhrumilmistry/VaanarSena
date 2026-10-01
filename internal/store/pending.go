package store

import "context"

// AddPendingIdentity records an identity certificate issued for a token.
func (s *Store) AddPendingIdentity(ctx context.Context, certSerial, tokenID string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO pending_identities (cert_serial, enrollment_token_id) VALUES ($1, $2)`, certSerial, tokenID)
	return err
}

// PendingIdentity returns the enrollment token bound to a certificate that
// has not completed enrollment.
func (s *Store) PendingIdentity(ctx context.Context, certSerial string) (*EnrollmentToken, error) {
	return scanET(s.DB.QueryRow(ctx, `SELECT `+prefixed("t.", etCols)+` FROM pending_identities p
		JOIN enrollment_tokens t ON t.id = p.enrollment_token_id WHERE p.cert_serial = $1`, certSerial))
}

// DeletePendingIdentity removes a pending identity once the device exists.
func (s *Store) DeletePendingIdentity(ctx context.Context, certSerial string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM pending_identities WHERE cert_serial = $1`, certSerial)
	return err
}

// PrunePendingIdentities drops identities never used within the window.
func (s *Store) PrunePendingIdentities(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM pending_identities WHERE created_at < now() - interval '7 days'`)
	return err
}

func prefixed(p, cols string) string {
	out := []byte{}
	start := true
	for i := 0; i < len(cols); i++ {
		c := cols[i]
		if start && c != ' ' && c != '\n' && c != '\t' {
			out = append(out, p...)
			start = false
		}
		out = append(out, c)
		if c == ',' {
			start = true
		}
	}
	return string(out)
}
