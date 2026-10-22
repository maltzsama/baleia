package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

const localeTestUserID = "11111111-1111-1111-1111-111111111111"

type fakeLocaleQuerier struct {
	db.Querier
	user    db.User
	updated *db.UpdateUserParams
}

func (f *fakeLocaleQuerier) GetUserByID(context.Context, pgtype.UUID) (db.User, error) {
	return f.user, nil
}

func (f *fakeLocaleQuerier) UpdateUser(_ context.Context, arg db.UpdateUserParams) (db.User, error) {
	f.updated = &arg
	u := f.user
	u.Name, u.Email, u.Locale = arg.Name, arg.Email, arg.Locale
	return u, nil
}

// TestPatchMeLocale: PATCH /me stores the chosen locale, rejects an unsupported
// one with a translatable field code, and leaves the preference untouched when
// the field is omitted (#67).
func TestPatchMeLocale(t *testing.T) {
	t.Run("stores a supported locale", func(t *testing.T) {
		fake := &fakeLocaleQuerier{user: db.User{ID: mustUUID(t, localeTestUserID)}}
		h := testHandlers(handlerFields{queries: fake, tx: directTx{q: fake}})
		locale := LocalePtBR

		resp, err := h.UpdateProfile(ctxAs(localeTestUserID, "member"), UpdateProfileRequestObject{
			Body: &UpdateProfileRequest{Locale: &locale},
		})
		if err != nil {
			t.Fatal(err)
		}
		ok, isOK := resp.(UpdateProfile200JSONResponse)
		if !isOK {
			t.Fatalf("UpdateProfile = %T, want 200", resp)
		}
		if fake.updated == nil || fake.updated.Locale == nil || *fake.updated.Locale != "pt-BR" {
			t.Fatalf("stored locale = %#v, want pt-BR", fake.updated)
		}
		if ok.Locale == nil || *ok.Locale != LocalePtBR {
			t.Fatalf("response locale = %v, want pt-BR", ok.Locale)
		}
	})

	t.Run("keeps the current locale when omitted", func(t *testing.T) {
		current := "en"
		fake := &fakeLocaleQuerier{user: db.User{ID: mustUUID(t, localeTestUserID), Locale: &current}}
		h := testHandlers(handlerFields{queries: fake, tx: directTx{q: fake}})

		if _, err := h.UpdateProfile(ctxAs(localeTestUserID, "member"), UpdateProfileRequestObject{
			Body: &UpdateProfileRequest{Name: ptr("New name")},
		}); err != nil {
			t.Fatal(err)
		}
		if fake.updated == nil || fake.updated.Locale == nil || *fake.updated.Locale != "en" {
			t.Fatalf("locale changed to %#v, want en", fake.updated)
		}
	})

	t.Run("rejects an unsupported locale", func(t *testing.T) {
		fake := &fakeLocaleQuerier{user: db.User{ID: mustUUID(t, localeTestUserID)}}
		h := testHandlers(handlerFields{queries: fake, tx: directTx{q: fake}})
		unsupported := Locale("fr")

		resp, err := h.UpdateProfile(ctxAs(localeTestUserID, "member"), UpdateProfileRequestObject{
			Body: &UpdateProfileRequest{Locale: &unsupported},
		})
		if err != nil {
			t.Fatal(err)
		}
		bad, isBad := resp.(UpdateProfile422JSONResponse)
		if !isBad {
			t.Fatalf("UpdateProfile = %T, want 422", resp)
		}
		if bad.Code != ErrorCodeValidationError || bad.Fields == nil || len(*bad.Fields) != 1 {
			t.Fatalf("422 = %#v, want one field error", bad)
		}
		field := (*bad.Fields)[0]
		if field.Field != "locale" || field.Code != FieldCodeNotAllowed {
			t.Fatalf("field = %#v, want locale/not_allowed", field)
		}
		if fake.updated != nil {
			t.Error("an invalid locale must not be stored")
		}
	})
}
