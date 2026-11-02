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
			if r.Kind != "function" || !r.Call {
				t.Fatalf("date_trunc = %+v", r)
			}
		}
		if r.Name == "current_date" && r.Call {
			t.Fatal("current_date is a special form without parentheses")
		}
	}
	if !found {
		t.Fatal("trino's reference lacks date_trunc")
	}
	if FunctionReferences("cobol") != nil {
		t.Fatal("an unknown dialect has no functions")
	}
}
