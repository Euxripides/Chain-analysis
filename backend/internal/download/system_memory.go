package download

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
)

type systemMemorySnapshot struct {
	WorkerRSSBytes      int64
	WorkerSwapBytes     int64
	VMMemAvailableBytes int64
	VMSwapUsedBytes     int64
	PSWAPIn             int64
	PSWAPOut            int64
	CgroupOOM           int64
	CgroupOOMKill       int64
}

type memoryGuard struct {
	mu                    sync.Mutex
	last                  systemMemorySnapshot
	haveLast              bool
	swapGrowthConsecutive int
	oomBaseline           int64
	oomKillBaseline       int64
}

func readKVFile(path string) (map[string]int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]int64{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) >= 3 && fields[2] == "kB" {
			v <<= 10
		}
		out[key] = v
	}
	return out, s.Err()
}

func readCgroupMemoryEvents() (oom, oomKill int64) {
	paths := []string{"/sys/fs/cgroup/memory.events", "/sys/fs/cgroup/memory/memory.oom_control"}
	for _, path := range paths {
		m, err := readKVFile(path)
		if err != nil {
			continue
		}
		if v, ok := m["oom"]; ok {
			oom += v
		}
		if v, ok := m["oom_kill"]; ok {
			oomKill += v
		}
	}
	return
}

func sampleSystemMemory() (systemMemorySnapshot, error) {
	var snap systemMemorySnapshot
	proc, err := readKVFile("/proc/self/status")
	if err != nil {
		return snap, err
	}
	mem, err := readKVFile("/proc/meminfo")
	if err != nil {
		return snap, err
	}
	vm, _ := readKVFile("/proc/vmstat")
	snap.WorkerRSSBytes = proc["VmRSS"]
	snap.WorkerSwapBytes = proc["VmSwap"]
	snap.VMMemAvailableBytes = mem["MemAvailable"]
	snap.VMSwapUsedBytes = mem["SwapTotal"] - mem["SwapFree"]
	snap.PSWAPIn = vm["pswpin"]
	snap.PSWAPOut = vm["pswpout"]
	snap.CgroupOOM, snap.CgroupOOMKill = readCgroupMemoryEvents()
	return snap, nil
}

func (g *memoryGuard) Evaluate(s systemMemorySnapshot, rssHard, memWarn, memProtect int64, growthSamples int) (ControlState, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if growthSamples < 2 {
		growthSamples = 2
	}
	if !g.haveLast {
		g.oomBaseline = s.CgroupOOM
		g.oomKillBaseline = s.CgroupOOMKill
	}
	// /proc/vmstat pswpin/pswpout are host-wide counters. Treating their
	// movement as worker pressure means an unrelated process can permanently
	// protect the downloader as soon as this process has any historical swap.
	// Require the worker's own VmSwap to grow; keep the system counters only as
	// telemetry so other workloads cannot trip this guard.
	swapGrowing := g.haveLast && s.WorkerSwapBytes > g.last.WorkerSwapBytes
	if swapGrowing {
		g.swapGrowthConsecutive++
	} else {
		g.swapGrowthConsecutive = 0
	}
	g.last = s
	g.haveLast = true

	switch {
	case s.CgroupOOM > g.oomBaseline || s.CgroupOOMKill > g.oomKillBaseline:
		return stateProtected, "cgroup OOM increment"
	case rssHard > 0 && s.WorkerRSSBytes >= rssHard:
		return stateProtected, "worker RSS hard max"
	case memProtect > 0 && s.VMMemAvailableBytes > 0 && s.VMMemAvailableBytes < memProtect:
		return stateProtected, "VM MemAvailable below protect threshold"
	case s.WorkerSwapBytes > 0 && g.swapGrowthConsecutive >= growthSamples:
		return stateProtected, "worker/system swap sustained growth"
	case memWarn > 0 && s.VMMemAvailableBytes > 0 && s.VMMemAvailableBytes < memWarn:
		return stateDegraded, "VM MemAvailable below warning threshold"
	default:
		return stateNormal, ""
	}
}

func (g *memoryGuard) Snapshot() systemMemorySnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last
}
