package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

type Generator interface {
	New(prefix string) (string, error)
}

type Crypto struct{}

func (Crypto) New(prefix string) (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(buf), nil
}

type Sequence struct {
	mu   sync.Mutex
	next int64
}

func NewSequence(start int64) *Sequence { return &Sequence{next: start} }

func (s *Sequence) New(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := fmt.Sprintf("%s_%08d", prefix, s.next)
	s.next++
	return value, nil
}
