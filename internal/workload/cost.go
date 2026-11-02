package workload

import "encoding/json"

// TrinoPlanCost returns the largest estimated byte count a Trino
// EXPLAIN (FORMAT JSON) plan reports (#550). ok is false when the plan carries
// no estimate (missing statistics), so classification falls through to the
// next step.
func TrinoPlanCost(plan []byte) (int64, bool) {
	var doc any
	if err := json.Unmarshal(plan, &doc); err != nil {
		return 0, false
	}
	var largest int64
	var found bool
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			for key, value := range n {
				switch key {
				case "outputSizeInBytes", "physicalInputSizeInBytes", "inputSizeInBytes":
					if f, ok := value.(float64); ok && f > 0 {
						if int64(f) > largest {
							largest = int64(f)
						}
						found = true
					}
				}
				walk(value)
			}
		case []any:
			for _, value := range n {
				walk(value)
			}
		}
	}
	walk(doc)
	return largest, found
}
