package dialect

import "testing"

func TestFunctionReferences(t *testing.T) {
	t.Parallel()
	refs := FunctionReferences(Trino)
	if len(refs) == 0 {
		t.Fatal("no functions")
	}
	found := false
	for _, r := range refs {
		if r.Name == "date_trunc" {
			found = true
			if r.Kind != "function" {
				t.Fatalf("kind = %q", r.Kind)
			}
		}
	}
	if !found {
		t.Fatal("trino's reference lacks date_trunc")
	}
	if FunctionReferences("cobol") != nil {
		t.Fatal("an unknown dialect has no functions")
	}
}
