package datapolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Store errors.
var (
	ErrNotFound        = errors.New("datapolicy: not found")
	ErrNameTaken       = errors.New("datapolicy: a policy of the table already has that name")
	ErrVersionConflict = errors.New("datapolicy: the policy changed since it was read")
)

// RevisionReason says why a revision was built.
type RevisionReason string

const (
	// ReasonPublish: an admin published the current policies.
	ReasonPublish RevisionReason = "publish"
	// ReasonReissue: the same content with a fresh lease.
	ReasonReissue RevisionReason = "reissue"
	// ReasonRollback: an earlier revision's content with a fresh lease.
	ReasonRollback RevisionReason = "rollback"
	// ReasonSubjects: the subjects changed (team membership, sign-in groups).
	ReasonSubjects RevisionReason = "subjects"
)

// Revision is one published state of the data policies (ADR 0028). It is
// built once and never rewritten: a re-issue or a rollback is a new revision
// whose SourceID names the one it copies. Number orders revisions.
type Revision struct {
	ID       uuid.UUID
	Number   int64
	Reason   RevisionReason
	SourceID *uuid.UUID
	// Content is the published policies, as JSON.
	Content []byte
	// Digest is the hex SHA-256 of Content.
	Digest    string
	NotAfter  time.Time
	CreatedBy *uuid.UUID
	CreatedAt time.Time
}

// NewRevision builds a revision of content with its digest. The store
// assigns its number.
func NewRevision(reason RevisionReason, content []byte, notAfter time.Time, createdBy *uuid.UUID) Revision {
	sum := sha256.Sum256(content)
	return Revision{
		ID:        uuid.New(),
		Reason:    reason,
		Content:   append([]byte(nil), content...),
		Digest:    hex.EncodeToString(sum[:]),
		NotAfter:  notAfter.UTC(),
		CreatedBy: createdBy,
	}
}
