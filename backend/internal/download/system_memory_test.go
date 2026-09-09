package download

import "testing"

func TestMemoryGuardRSSProtect(t *testing.T) {
	g := &memoryGuard{}
	state, reason := g.Evaluate(systemMemorySnapshot{WorkerRSSBytes: 12 << 30, VMMemAvailableBytes: 20 << 30}, 12<<30, 6<<30, 4<<30, 2)
	if state != stateProtected || reason == "" {
		t.Fatalf("state=%v reason=%q", state, reason)
	}
}

func TestMemoryGuardMemAvailableThresholds(t *testing.T) {
	g := &memoryGuard{}
	state, _ := g.Evaluate(systemMemorySnapshot{VMMemAvailableBytes: 5 << 30}, 12<<30, 6<<30, 4<<30, 2)
	if state != stateDegraded {
		t.Fatalf("warning state=%v", state)
	}
	state, _ = g.Evaluate(systemMemorySnapshot{VMMemAvailableBytes: 3 << 30}, 12<<30, 6<<30, 4<<30, 2)
	if state != stateProtected {
		t.Fatalf("protect state=%v", state)
	}
}

func TestMemoryGuardRequiresSustainedSwapGrowth(t *testing.T) {
	g := &memoryGuard{}
	base := systemMemorySnapshot{WorkerSwapBytes: 1, VMMemAvailableBytes: 20 << 30}
	if state, _ := g.Evaluate(base, 12<<30, 6<<30, 4<<30, 2); state != stateNormal {
		t.Fatalf("baseline state=%v", state)
	}
	base.WorkerSwapBytes++
	if state, _ := g.Evaluate(base, 12<<30, 6<<30, 4<<30, 2); state != stateNormal {
		t.Fatalf("single growth state=%v", state)
	}
	base.WorkerSwapBytes++
	if state, _ := g.Evaluate(base, 12<<30, 6<<30, 4<<30, 2); state != stateProtected {
		t.Fatalf("sustained growth state=%v", state)
	}
}

func TestMemoryGuardOOMIncrement(t *testing.T) {
	g := &memoryGuard{}
	base := systemMemorySnapshot{VMMemAvailableBytes: 20 << 30, CgroupOOM: 3}
	g.Evaluate(base, 12<<30, 6<<30, 4<<30, 2)
	base.CgroupOOM = 4
	if state, _ := g.Evaluate(base, 12<<30, 6<<30, 4<<30, 2); state != stateProtected {
		t.Fatalf("OOM state=%v", state)
	}
}
