package pipeline

import (
	"testing"
	"time"
)

func TestStateTransitionParity(t *testing.T) {
	opened := time.Date(2026, 8, 6, 3, 0, 0, 0, time.UTC)
	if got := chooseStateAction(nil, "firing", opened, defaultFlapWindow); got != actionCreate {
		t.Fatalf("first firing: got %v", got)
	}
	active := &problem{Status: "OPEN", OpenedAt: opened, LastSeenAt: opened}
	if got := chooseStateAction(active, "firing", opened.Add(30*time.Second), defaultFlapWindow); got != actionRepeat {
		t.Fatalf("repeat firing: got %v", got)
	}
	if got := chooseStateAction(active, "resolved", opened.Add(time.Minute), defaultFlapWindow); got != actionResolve {
		t.Fatalf("resolve: got %v", got)
	}
	resolvedAt := opened.Add(time.Minute)
	resolved := &problem{Status: "RESOLVED", ResolvedAt: &resolvedAt}
	if got := chooseStateAction(resolved, "firing", resolvedAt.Add(40*time.Second), defaultFlapWindow); got != actionReopen {
		t.Fatalf("reopen in flap window: got %v", got)
	}
	if got := chooseStateAction(resolved, "firing", resolvedAt.Add(5*time.Minute), defaultFlapWindow); got != actionCreate {
		t.Fatalf("new episode outside flap window: got %v", got)
	}
	if got := chooseStateAction(resolved, "resolved", resolvedAt.Add(time.Minute), defaultFlapWindow); got != actionNoop {
		t.Fatalf("orphan resolve: got %v", got)
	}
}

func TestTTLBySymptom(t *testing.T) {
	if ttlForSymptom("host_unreachable") != 10*time.Minute {
		t.Fatal("wrong host TTL")
	}
	if ttlForSymptom("disk_space") != 30*time.Minute {
		t.Fatal("wrong disk TTL")
	}
	if ttlForSymptom("new_class") != 20*time.Minute {
		t.Fatal("unknown symptoms must use fallback TTL")
	}
}
