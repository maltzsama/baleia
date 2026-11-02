package workload

import "testing"

func TestTrinoPlanCost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		plan string
		want int64
		ok   bool
	}{
		{"estimates", `{"outputStage":{"estimates":[{"outputSizeInBytes":123456}]}}`, 123456, true},
		{"largest of the plan", `{"a":{"outputSizeInBytes":10},"b":[{"inputSizeInBytes":20}]}`, 20, true},
		{"no statistics", `{"outputStage":{}}`, 0, false},
		{"invalid json", `not json`, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := TrinoPlanCost([]byte(tc.plan))
			if got != tc.want || ok != tc.ok {
				t.Fatalf("TrinoPlanCost = %d/%v, want %d/%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
