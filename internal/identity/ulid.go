package identity

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// NewULID creates a lexically sortable platform identifier.
func NewULID(now time.Time) (string, error) {
	id, err := ulid.New(ulid.Timestamp(now.UTC()), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ULID: %w", err)
	}

	return id.String(), nil
}

// Generator generates production ULIDs.
type Generator struct{}

// New returns a new ULID for the supplied timestamp.
func (Generator) New(now time.Time) (string, error) {
	return NewULID(now)
}
