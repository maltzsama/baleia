package datapolicy

import (
	"slices"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/trinoidentity"
)

// SubjectSource is one active account as the subject map needs it: its id,
// e-mail, kind, teams and the identity-provider groups of its latest sign-in.
type SubjectSource struct {
	UserID         db.UUID
	Email          string
	ServiceAccount bool
	Teams          []string
	Groups         []string
}

// Subjects builds the bundle's subject map: each active account's Trino
// identity, produced by the same mapping as its queries
// (BALEIA_TRINO_USER_IDENTITY, trinoidentity.Of), with its teams and groups.
// Humans are named by user id, service accounts by service account id, as
// policy subjects reference them. Suspended and deleted accounts are not in
// sources, so they match only "everyone" references, and their sessions are
// already gone (ADR 0016).
func Subjects(mode string, sources []SubjectSource) map[string]SubjectEntry {
	out := make(map[string]SubjectEntry, len(sources))
	for _, s := range sources {
		entry := SubjectEntry{
			Teams:  sortedOrEmpty(s.Teams),
			Groups: sortedOrEmpty(s.Groups),
		}
		if s.ServiceAccount {
			entry.ServiceAccount = s.UserID.String()
		} else {
			entry.User = s.UserID.String()
		}
		out[trinoidentity.Of(mode, s.UserID, s.Email)] = entry
	}
	return out
}

func sortedOrEmpty(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return slices.Compact(slices.Sorted(slices.Values(values)))
}
