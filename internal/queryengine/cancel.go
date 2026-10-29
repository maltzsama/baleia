package queryengine

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// A session reports a statement's handle in OnStart, before the statement
// reaches the server (#505). A cancel from another process that arrives in
// that gap finds nothing running; on engines that cancel "whatever the
// session runs now" it would be lost, and the statement would run to the
// end. Those engines tag each statement and cancel only the tagged one,
// retrying for a short window until it shows up.

// StatementTagKey is the QueryHandle.Extra key that carries the tag.
const StatementTagKey = "statement_tag"

// CancelWindow is how long a cancel keeps looking for a statement it did not
// find; CancelMinAttempt is the least time one attempt gets, so the last one
// is not cut short.
const (
	CancelWindow     = 3 * time.Second
	CancelMinAttempt = time.Second
)

// NewStatementTag returns a tag unique to one statement.
func NewStatementTag() string { return uuid.NewString() }

// TagStatement prefixes sql with a comment carrying tag. The engine keeps the
// comment in the text it shows for running statements, which is how a
// cancel finds this statement and no other.
func TagStatement(sql, tag string) string {
	return "/* baleia:" + tag + " */ " + sql
}

// TagPattern is the SQL LIKE pattern that matches the text of a statement
// tagged with tag. Tags are UUIDs: they hold no LIKE wildcards.
func TagPattern(tag string) string {
	return "/* baleia:" + strings.NewReplacer("%", "", "_", "").Replace(tag) + " */%"
}

// CancelUntilFound runs cancel until it reports that it cancelled the
// statement, for up to window. A statement still missing after the window
// has finished, which is not an error. cancel gets the time left (at least
// CancelMinAttempt), so a stalled attempt cannot stretch the call much past
// the window.
func CancelUntilFound(window time.Duration, cancel func(left time.Duration) (bool, error)) error {
	deadline := time.Now().Add(window)
	wait := 50 * time.Millisecond
	for {
		done, err := cancel(max(time.Until(deadline), CancelMinAttempt))
		if err != nil || done || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(min(wait, time.Until(deadline)))
		wait = min(wait*2, 400*time.Millisecond)
	}
}
