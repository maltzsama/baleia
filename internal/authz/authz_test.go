package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func uid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

type store struct {
	conns map[[16]byte]db.CatalogConnection
	cats  map[[16]byte]db.Catalog
}

func (s store) GetCatalogConnectionByID(_ context.Context, id pgtype.UUID) (db.CatalogConnection, error) {
	if c, ok := s.conns[id.Bytes]; ok {
		return c, nil
	}
	return db.CatalogConnection{}, pgx.ErrNoRows
}

func (s store) GetCatalogByID(_ context.Context, id pgtype.UUID) (db.Catalog, error) {
	if c, ok := s.cats[id.Bytes]; ok {
		return c, nil
	}
	return db.Catalog{}, pgx.ErrNoRows
}

var (
	owner  = Principal{UserID: uid(1), Role: "member"}
	other  = Principal{UserID: uid(2), Role: "member"}
	admin  = Principal{UserID: uid(3), Role: RoleAdmin}
	connA  = uid(10) // owned by owner
	connB  = uid(11) // owned by other
	connNo = uid(12) // created_by missing (legacy row)
	catA   = uid(20) // under connA
)

func fixture() *Checker {
	return New(store{
		conns: map[[16]byte]db.CatalogConnection{
			connA.Bytes:  {ID: connA, CreatedBy: owner.UserID},
			connB.Bytes:  {ID: connB, CreatedBy: other.UserID},
			connNo.Bytes: {ID: connNo},
		},
		cats: map[[16]byte]db.Catalog{catA.Bytes: {ID: catA, ConnectionID: connA}},
	})
}

func TestConnection(t *testing.T) {
	c := fixture()
	ctx := context.Background()
	cases := []struct {
		name string
		p    Principal
		conn pgtype.UUID
		want error
	}{
		{"owner", owner, connA, nil},
		{"global admin", admin, connA, nil},
		{"foreign user is told it does not exist", other, connA, ErrNotFound},
		{"missing connection", owner, uid(99), ErrNotFound},
		{"legacy row without created_by matches nobody", owner, connNo, ErrNotFound},
		{"legacy row is still reachable by an admin", admin, connNo, nil},
	}
	for _, tc := range cases {
		for _, act := range []Action{ActRead, ActWrite, ActAdmin} {
			if _, err := c.Connection(ctx, tc.p, tc.conn, act); !errors.Is(err, tc.want) {
				t.Errorf("%s (act %d): err = %v, want %v", tc.name, act, err, tc.want)
			}
		}
	}
}

func TestCatalog(t *testing.T) {
	c := fixture()
	ctx := context.Background()
	if cat, conn, err := c.Catalog(ctx, owner, catA, ActWrite); err != nil || cat.ID != catA || conn.ID != connA {
		t.Fatalf("owner: %v %v %v", cat.ID, conn.ID, err)
	}
	if _, _, err := c.Catalog(ctx, admin, catA, ActWrite); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if _, _, err := c.Catalog(ctx, other, catA, ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign: %v, want ErrNotFound", err)
	}
	if _, _, err := c.Catalog(ctx, owner, uid(98), ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing catalog: %v", err)
	}
}

// TestCatalogOfConnection: a catalog id must be reached through its own
// connection, so a connection you own cannot be used to reach another's catalog.
func TestCatalogOfConnection(t *testing.T) {
	c := fixture()
	ctx := context.Background()
	if _, _, err := c.CatalogOfConnection(ctx, owner, connA, catA, ActRead); err != nil {
		t.Fatalf("matching connection: %v", err)
	}
	// 'other' owns connB, but catA belongs to connA.
	if _, _, err := c.CatalogOfConnection(ctx, other, connB, catA, ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("catalog of another connection under an owned connection: %v, want ErrNotFound", err)
	}
	// Even an admin cannot address a catalog through the wrong connection.
	if _, _, err := c.CatalogOfConnection(ctx, admin, connB, catA, ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("admin with mismatched connection: %v, want ErrNotFound", err)
	}
}

func TestRequireAdmin(t *testing.T) {
	if err := admin.RequireAdmin(); err != nil {
		t.Fatal(err)
	}
	if err := owner.RequireAdmin(); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member: %v, want ErrForbidden", err)
	}
}

func TestPrincipalFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), auth.ContextKeyUserID, "11111111-1111-1111-1111-111111111111")
	ctx = context.WithValue(ctx, auth.ContextKeyRole, "admin")
	p, err := PrincipalFromContext(ctx)
	if err != nil || !p.IsAdmin() || !p.UserID.Valid {
		t.Fatalf("PrincipalFromContext = %+v, %v", p, err)
	}
	for _, bad := range []context.Context{
		context.Background(),
		context.WithValue(context.Background(), auth.ContextKeyUserID, "not-a-uuid"),
	} {
		if _, err := PrincipalFromContext(bad); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("bad context: %v, want ErrUnauthenticated", err)
		}
	}
}
