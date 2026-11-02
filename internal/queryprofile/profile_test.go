package queryprofile

import "testing"

// TestFromPayload_Event: the listener's profile carries the timeline and the
// input table names (#532).
func TestFromPayload_Event(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"source":"query_completed_event","state":"FINISHED","queuedMs":10,` +
		`"analysisMs":3,"planningMs":20,"executionMs":65,"cpuMs":50,` +
		`"peakMemoryBytes":4096,"spilledBytes":0,"inputBytes":5000,"inputRows":100,` +
		`"outputBytes":200,"outputRows":10,` +
		`"inputs":[{"catalog":"lake","schema":"sales","table":"orders"}]}`)
	p := FromPayload(raw)
	if p == nil {
		t.Fatal("nil profile")
	}
	if p.QueuedMs == nil || *p.QueuedMs != 10 || p.PlanningMs == nil || *p.PlanningMs != 20 || p.ExecutionMs == nil || *p.ExecutionMs != 65 {
		t.Fatalf("timeline = %+v", p)
	}
	if len(p.Inputs) != 1 || p.Inputs[0].Table != "orders" {
		t.Fatalf("inputs = %+v", p.Inputs)
	}
}

// TestFromPayload_QueryInfo: the poller's /v1/query list/detail shape.
func TestFromPayload_QueryInfo(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"queryId":"q","state":"FINISHED","queryStats":{` +
		`"queuedTime":"10.00ms","planningTime":"20.00ms","executionTime":"65.00ms",` +
		`"totalCpuTime":"50.00ms","physicalInputDataSize":"5.00kB","outputPositions":10,` +
		`"inputs":[{"catalogName":"lake","schema":"sales","table":"orders",` +
		`"physicalInputBytes":"4.00kB","physicalInputPositions":90}]}}`)
	p := FromPayload(raw)
	if p == nil {
		t.Fatal("nil profile")
	}
	if p.QueuedMs == nil || *p.QueuedMs != 10 || p.ExecutionMs == nil || *p.ExecutionMs != 65 {
		t.Fatalf("timeline = %+v", p)
	}
	if p.CPUMs == nil || *p.CPUMs != 50 {
		t.Fatalf("cpu = %v", p.CPUMs)
	}
	if len(p.Inputs) != 1 || p.Inputs[0].Bytes == nil || *p.Inputs[0].Bytes != 4096 {
		t.Fatalf("inputs = %+v", p.Inputs)
	}
}

func TestFromPayload_Empty(t *testing.T) {
	t.Parallel()
	if FromPayload(nil) != nil || FromPayload([]byte(`{}`)) != nil {
		t.Fatal("an unknown payload has no profile")
	}
}
