package auth

import (
	"crypto/rand"
	"encoding/base64"
)

type SessionStore struct {
	store map[string]string
}

func NewSessionStore() *SessionStore {
	return &SessionStore{store: map[string]string{}}
}

func (s *SessionStore) CreateSession(userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.store[token] = userID
	return token, nil
}

func (s *SessionStore) GetUserID(token string) (string, bool) {
	userID, ok := s.store[token]
	return userID, ok
}

func (s *SessionStore) DeleteSession(token string) {
	delete(s.store, token)
}
