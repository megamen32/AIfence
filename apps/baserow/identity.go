package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/airlockrun/agentsdk"
)

type identityStore struct{ db *agentsdk.AgentDB }

func (s identityStore) seed(ctx context.Context) ([]byte, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return nil, e
	}
	if _, e := s.db.ExecContext(ctx, "INSERT INTO native_identity_seed(id, seed) VALUES (1,$1) ON CONFLICT (id) DO NOTHING", b); e != nil {
		return nil, e
	}
	var out []byte
	if e := s.db.QueryRowContext(ctx, "SELECT seed FROM native_identity_seed WHERE id=1").Scan(&out); e != nil {
		return nil, e
	}
	if len(out) != 32 {
		return nil, errors.New("Invalid identity seed")
	}
	return out, nil
}
func credentials(seed []byte, namespace, id string) (string, string) {
	h := sha256.Sum256([]byte(namespace + "\x00" + id))
	name := "al-" + hex.EncodeToString(h[:16])
	mac := hmac.New(sha256.New, seed)
	mac.Write([]byte(namespace + "\x00" + id))
	return name, "Aa1!" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func currentUser(ctx context.Context) (string, error) {
	c := agentsdk.CallerFromContext(ctx)
	u, ok := c.User()
	if !ok || u.ID == "" || !u.PlatformMember || (c.Access() != agentsdk.AccessUser && c.Access() != agentsdk.AccessAdmin) {
		return "", errors.New("Требуется личная учётная запись Airlock")
	}
	return u.ID, nil
}
