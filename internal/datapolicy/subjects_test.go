package datapolicy

import (
	"slices"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

func TestSubjects(t *testing.T) {
	t.Parallel()
	ana := db.UUID{Bytes: uuid.New(), Valid: true}
	bot := db.UUID{Bytes: uuid.New(), Valid: true}
	sources := []SubjectSource{
		{UserID: ana, Email: "ana@x.io", Teams: []string{"t2", "t1", "t1"}, Groups: []string{"analysts"}},
		{UserID: bot, Email: "bot@x.io", ServiceAccount: true},
	}

	byUUID := Subjects("uuid", sources)
	anaEntry, ok := byUUID["u_"+ana.String()]
	if !ok || anaEntry.User != ana.String() || anaEntry.ServiceAccount != "" {
		t.Fatalf("ana = %+v (%v)", anaEntry, ok)
	}
	if !slices.Equal(anaEntry.Teams, []string{"t1", "t2"}) || !slices.Equal(anaEntry.Groups, []string{"analysts"}) {
		t.Fatalf("ana teams/groups = %v %v", anaEntry.Teams, anaEntry.Groups)
	}
	botEntry := byUUID["u_"+bot.String()]
	if botEntry.ServiceAccount != bot.String() || botEntry.User != "" || botEntry.Teams == nil || botEntry.Groups == nil {
		t.Fatalf("service account = %+v, want its id and empty, non-null lists", botEntry)
	}

	byEmail := Subjects("email", sources)
	if _, ok := byEmail["ana@x.io"]; !ok || len(byEmail) != 2 {
		t.Fatalf("email identities = %v", byEmail)
	}
}
