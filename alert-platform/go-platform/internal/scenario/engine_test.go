package scenario

import "testing"

func TestParseAllowsBranchesAndRejectsCycles(t *testing.T) {
	valid := `{"nodes":[{"id":"c","type":"condition","data":{}},{"id":"a","type":"ack_check","data":{}},{"id":"n","type":"notify","data":{}}],"edges":[{"source":"c","target":"a"},{"source":"a","target":"n","sourceHandle":"yes"}]}`
	if _, ok := Parse(valid); !ok {
		t.Fatal("valid branched graph rejected")
	}
	cyclic := `{"nodes":[{"id":"c","type":"condition","data":{}},{"id":"n","type":"notify","data":{}}],"edges":[{"source":"c","target":"n"},{"source":"n","target":"c"}]}`
	if _, ok := Parse(cyclic); ok {
		t.Fatal("cyclic graph accepted")
	}
}
