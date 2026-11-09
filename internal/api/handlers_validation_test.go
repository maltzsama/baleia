package api

import (
	"testing"

	"github.com/galileostudio/baleia/internal/config"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// catalogRequest builds a CreateCatalogRequest with one output per (name,
// trino_catalog_name) pair.
func catalogRequest(pairs ...string) *CreateCatalogRequest {
	type item = struct {
		Name             string `json:"name"`
		TrinoCatalogName string `json:"trino_catalog_name"`
	}
	outs := make([]item, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		outs = append(outs, item{pairs[i], pairs[i+1]})
	}
	return &CreateCatalogRequest{Catalogs: outs}
}

func TestCreateApiKey_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{})
	resp, err := h.CreateApiKey(testContext(), CreateApiKeyRequestObject{Body: &CreateApiKeyRequest{Name: " "}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := resp.(CreateApiKey422JSONResponse); !ok || got.Code != ErrorCodeValidationError {
		t.Fatalf("want 422 validation_error, got %T %+v", resp, resp)
	}
}

func TestCreateWorkspace_RejectsBadNameAndType(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{})

	resp, err := h.CreateWorkspace(testContext(), CreateWorkspaceRequestObject{Body: &CreateWorkspaceRequest{
		Name: " ", Type: CreateWorkspaceRequestTypePersonal,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(CreateWorkspace422JSONResponse); !ok {
		t.Fatalf("empty name: %T, want 422", resp)
	}

	resp, err = h.CreateWorkspace(testContext(), CreateWorkspaceRequestObject{Body: &CreateWorkspaceRequest{
		Name: "ok", Type: CreateWorkspaceRequestType("nope"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(CreateWorkspace422JSONResponse); !ok {
		t.Fatalf("bad type: %T, want 422", resp)
	}
}

func TestCreateTeam_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{})
	resp, err := h.CreateTeam(ctxAs(ownerID, "admin"), CreateTeamRequestObject{Body: &CreateTeamRequest{Name: " "}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(CreateTeam422JSONResponse); !ok {
		t.Fatalf("want 422, got %T", resp)
	}
}

func TestCreateCatalogConnection_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	h := testAPI(handlerFields{settings: SettingsFromConfig(&config.Config{ConnectionsCreate: "everyone"})})
	resp, err := h.CreateCatalogConnection(ctxAs(ownerID, "member"), CreateCatalogConnectionRequestObject{
		Body: &CreateCatalogConnectionRequest{Name: " "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(CreateCatalogConnection422JSONResponse); !ok {
		t.Fatalf("want 422, got %T", resp)
	}
}

func TestCreateCatalog_RejectsInvalidIdentifier(t *testing.T) {
	t.Parallel()
	h, q, _ := newAuthzHandlers(t)
	conn, _ := reqIDs(q)
	resp, err := h.CreateCatalog(ctxAs(ownerID, "member"), CreateCatalogRequestObject{ConnectionId: conn, Body: catalogRequest("x", "Bad-Name")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(CreateCatalog422JSONResponse); !ok {
		t.Fatalf("want 422, got %T", resp)
	}
}

func TestCreateCatalogBinding_RejectsInvalidEngineCatalogName(t *testing.T) {
	t.Parallel()
	q := newBindingQuerier(t)
	h, _ := bindingHandlers(t, q)
	body := CreateBindingRequest{EngineId: openapi_types.UUID(bEngine.Bytes), EngineCatalogName: "Bad-Name"}
	if c, _ := createBinding(t, h, ctxAs(adminID, "admin"), body); c != 422 {
		t.Fatalf("want 422, got %d", c)
	}
}
