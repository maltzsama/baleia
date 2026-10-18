package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func fakeIDPProbe(_ context.Context, _ providerConfig, _ string) IdentityProviderTestResult {
	return idpTestResult(idpCheck{name: "fake", status: idpCheckOK})
}

func createOIDCProvider(t *testing.T, h *Handlers) IdentityProviderDetail {
	t.Helper()
	resp, err := h.CreateIdentityProvider(idpAdminCtx(), CreateIdentityProviderRequestObject{Body: &CreateIdentityProviderRequest{
		Name: "okta", Type: IdentityProviderTypeOidc, Oidc: validOIDCConfig(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := resp.(CreateIdentityProvider201JSONResponse)
	if !ok {
		t.Fatalf("create = %T", resp)
	}
	return IdentityProviderDetail(created)
}

// TestIdP_PatchKeepsSecret: a PATCH that omits client_secret keeps the stored
// value and only changes the fields that were sent.
func TestIdP_PatchKeepsSecret(t *testing.T) {
	h := idpTestHandlers(t)
	detail := createOIDCProvider(t, h)

	issuer := "https://other.example.test"
	name := "renamed"
	got, err := h.UpdateIdentityProvider(idpAdminCtx(), UpdateIdentityProviderRequestObject{
		Id: detail.Id,
		Body: &UpdateIdentityProviderRequest{
			Name: &name,
			Oidc: &UpdateOIDCConfig{IssuerUrl: &issuer},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := got.(UpdateIdentityProvider200JSONResponse)
	if !ok {
		t.Fatalf("patch = %T", got)
	}
	if updated.Name != "renamed" || updated.Oidc == nil || updated.Oidc.IssuerUrl == nil || *updated.Oidc.IssuerUrl != issuer {
		t.Fatalf("updated = %+v", updated)
	}
	if !updated.HasClientSecret {
		t.Error("has_client_secret = false after a patch that omitted the secret")
	}

	ap, err := h.queries.GetAuthProviderByID(context.Background(), mustUUID(t, detail.Id.String()))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := auth.OpenOIDCConfig(h.keyring, idString(ap.ID), ap.ConfigEnc)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientSecret != "oidc-client-secret" {
		t.Errorf("stored secret = %q, want the original", cfg.ClientSecret)
	}
}

// TestIdP_DeleteWithUsers409: a provider with linked users cannot be deleted
// without the explicit suspension flag.
func TestIdP_DeleteWithUsers409(t *testing.T) {
	h := idpTestHandlers(t)
	detail := createOIDCProvider(t, h)
	ctx := context.Background()
	apID := mustUUID(t, detail.Id.String())
	if _, err := h.pool.Exec(ctx, `INSERT INTO users (name, email, auth_provider, auth_provider_id, external_id)
		VALUES ('dir user', 'dir@example.test', 'oidc', $1, 'ext-1')`, apID); err != nil {
		t.Fatal(err)
	}

	// The admin list/detail reports how many users are linked (#265).
	gotDetail, err := h.GetIdentityProvider(idpAdminCtx(), GetIdentityProviderRequestObject{Id: detail.Id})
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := gotDetail.(GetIdentityProvider200JSONResponse); !ok || d.LinkedUsers != 1 {
		t.Fatalf("detail = %#v, want linked_users=1", gotDetail)
	}

	got, err := h.DeleteIdentityProvider(idpAdminCtx(), DeleteIdentityProviderRequestObject{Id: detail.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(DeleteIdentityProvider409JSONResponse); !ok {
		t.Fatalf("delete = %T, want 409", got)
	}
}

// TestIdP_DeleteSuspends: with suspend_users=true the linked users are
// suspended and detached before the provider row is removed.
func TestIdP_DeleteSuspends(t *testing.T) {
	h := idpTestHandlers(t)
	detail := createOIDCProvider(t, h)
	ctx := context.Background()
	apID := mustUUID(t, detail.Id.String())
	if _, err := h.pool.Exec(ctx, `INSERT INTO users (name, email, auth_provider, auth_provider_id, external_id)
		VALUES ('dir user', 'dir@example.test', 'oidc', $1, 'ext-1')`, apID); err != nil {
		t.Fatal(err)
	}

	suspend := true
	got, err := h.DeleteIdentityProvider(idpAdminCtx(), DeleteIdentityProviderRequestObject{
		Id: detail.Id, Params: DeleteIdentityProviderParams{SuspendUsers: &suspend},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(DeleteIdentityProvider204Response); !ok {
		t.Fatalf("delete = %T, want 204", got)
	}

	var status, provider string
	if err := h.pool.QueryRow(ctx, `SELECT status, auth_provider FROM users WHERE email = 'dir@example.test'`).Scan(&status, &provider); err != nil {
		t.Fatal(err)
	}
	if status != auth.StatusSuspended || provider != "local" {
		t.Errorf("user = %s/%s, want suspended/local", status, provider)
	}

	var n int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM auth_providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("auth_providers rows = %d, want 0", n)
	}
}

// TestIdP_TransientTestPersistsNothing: an unsaved configuration is probed and
// discarded.
func TestIdP_TransientTestPersistsNothing(t *testing.T) {
	h := idpTestHandlers(t)
	h.idpProbe = fakeIDPProbe

	got, err := h.TestIdentityProviderTransient(idpAdminCtx(), TestIdentityProviderTransientRequestObject{
		Body: &TestIdentityProviderRequest{Type: IdentityProviderTypeOidc, Oidc: validOIDCConfig()},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := got.(TestIdentityProviderTransient200JSONResponse)
	if !ok {
		t.Fatalf("transient test = %T", got)
	}
	if !result.Ok || len(result.Checks) == 0 {
		t.Fatalf("result = %+v", result)
	}

	var n int
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM auth_providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("auth_providers rows = %d, want 0", n)
	}
}

// TestIdP_TestStoredProvider: the stored probe loads the sealed config and
// returns a check list without writing.
func TestIdP_TestStoredProvider(t *testing.T) {
	h := idpTestHandlers(t)
	h.idpProbe = fakeIDPProbe
	detail := createOIDCProvider(t, h)

	got, err := h.TestIdentityProvider(idpAdminCtx(), TestIdentityProviderRequestObject{Id: detail.Id})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := got.(TestIdentityProvider200JSONResponse)
	if !ok {
		t.Fatalf("stored test = %T", got)
	}
	if !result.Ok {
		t.Fatalf("result = %+v", result)
	}
}

// TestIdP_MutationsNonAdmin403: the new admin operations refuse a member.
func TestIdP_MutationsNonAdmin403(t *testing.T) {
	h := idpTestHandlers(t)
	ctx := idpMemberCtx()
	id := openapi_types.UUID(uuid.New())

	if got, _ := h.UpdateIdentityProvider(ctx, UpdateIdentityProviderRequestObject{Id: id, Body: &UpdateIdentityProviderRequest{}}); !isUpdate403(got) {
		t.Errorf("patch = %T, want 403", got)
	}
	if got, _ := h.DeleteIdentityProvider(ctx, DeleteIdentityProviderRequestObject{Id: id}); !isDelete403(got) {
		t.Errorf("delete = %T, want 403", got)
	}
	if got, _ := h.TestIdentityProvider(ctx, TestIdentityProviderRequestObject{Id: id}); !isTest403(got) {
		t.Errorf("test = %T, want 403", got)
	}
	if got, _ := h.TestIdentityProviderTransient(ctx, TestIdentityProviderTransientRequestObject{Body: &TestIdentityProviderRequest{Type: IdentityProviderTypeOidc, Oidc: validOIDCConfig()}}); !isTransient403(got) {
		t.Errorf("transient = %T, want 403", got)
	}
}

func isUpdate403(v UpdateIdentityProviderResponseObject) bool {
	_, ok := v.(UpdateIdentityProvider403JSONResponse)
	return ok
}

func isDelete403(v DeleteIdentityProviderResponseObject) bool {
	_, ok := v.(DeleteIdentityProvider403JSONResponse)
	return ok
}

func isTest403(v TestIdentityProviderResponseObject) bool {
	_, ok := v.(TestIdentityProvider403JSONResponse)
	return ok
}

func isTransient403(v TestIdentityProviderTransientResponseObject) bool {
	_, ok := v.(TestIdentityProviderTransient403JSONResponse)
	return ok
}
