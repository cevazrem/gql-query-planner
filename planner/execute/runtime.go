package execute

import (
	"sync"
	"time"
)

type NodeRuntimeStats struct {
	ActualCalls            int64
	ActualPhysicalCalls    int64
	ActualRowsOut          int64
	ActualBatchCalls       int64
	TotalResolverNanos     int64
	ObservedWidthBytes     int64
	MaxObservedParallelism int64

	CacheLookups int64
	CacheHits    int64
	CacheMisses  int64
	CacheWaits   int64

	currentParallelism int64
}

type Runtime struct {
	mu     sync.Mutex
	byNode map[string]*NodeRuntimeStats
}

func NewRuntime() *Runtime {
	return &Runtime{byNode: make(map[string]*NodeRuntimeStats)}
}

func (rt *Runtime) get(nodeID string) *NodeRuntimeStats {
	st, ok := rt.byNode[nodeID]
	if !ok {
		st = &NodeRuntimeStats{}
		rt.byNode[nodeID] = st
	}
	return st
}

func (rt *Runtime) AddLogicalCalls(nodeID string, n int64) {
	if rt == nil || nodeID == "" || n <= 0 {
		return
	}
	rt.mu.Lock()
	rt.get(nodeID).ActualCalls += n
	rt.mu.Unlock()
}

func (rt *Runtime) AddRowsOut(nodeID string, n int64) {
	if rt == nil || nodeID == "" || n <= 0 {
		return
	}
	rt.mu.Lock()
	rt.get(nodeID).ActualRowsOut += n
	rt.mu.Unlock()
}

func (rt *Runtime) AddWidthBytes(nodeID string, n int64) {
	if rt == nil || nodeID == "" || n < 0 {
		return
	}
	rt.mu.Lock()
	rt.get(nodeID).ObservedWidthBytes += n
	rt.mu.Unlock()
}

type CacheLookupOutcome uint8

const (
	CacheLookupHit CacheLookupOutcome = iota + 1
	CacheLookupMiss
	CacheLookupWait
)

func (rt *Runtime) AddCacheLookup(nodeID string, outcome CacheLookupOutcome) {
	if rt == nil || nodeID == "" || outcome == 0 {
		return
	}
	rt.mu.Lock()
	st := rt.get(nodeID)
	st.CacheLookups++
	switch outcome {
	case CacheLookupHit:
		st.CacheHits++
	case CacheLookupMiss:
		st.CacheMisses++
	case CacheLookupWait:
		st.CacheWaits++
	}
	rt.mu.Unlock()
}

func (rt *Runtime) BeginPhysical(nodeID string) func() {
	if rt == nil || nodeID == "" {
		return func() {}
	}
	start := time.Now()
	rt.mu.Lock()
	st := rt.get(nodeID)
	st.ActualPhysicalCalls++
	st.currentParallelism++
	if st.currentParallelism > st.MaxObservedParallelism {
		st.MaxObservedParallelism = st.currentParallelism
	}
	rt.mu.Unlock()

	return func() {
		dur := time.Since(start).Nanoseconds()
		rt.mu.Lock()
		st := rt.get(nodeID)
		st.TotalResolverNanos += dur
		if st.currentParallelism > 0 {
			st.currentParallelism--
		}
		rt.mu.Unlock()
	}
}

func (rt *Runtime) BeginBatchPhysical(nodeID string) func() {
	if rt == nil || nodeID == "" {
		return func() {}
	}
	start := time.Now()
	rt.mu.Lock()
	st := rt.get(nodeID)
	st.ActualPhysicalCalls++
	st.ActualBatchCalls++
	st.currentParallelism++
	if st.currentParallelism > st.MaxObservedParallelism {
		st.MaxObservedParallelism = st.currentParallelism
	}
	rt.mu.Unlock()

	return func() {
		dur := time.Since(start).Nanoseconds()
		rt.mu.Lock()
		st := rt.get(nodeID)
		st.TotalResolverNanos += dur
		if st.currentParallelism > 0 {
			st.currentParallelism--
		}
		rt.mu.Unlock()
	}
}

func (rt *Runtime) Snapshot() map[string]NodeRuntimeStats {
	if rt == nil {
		return nil
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make(map[string]NodeRuntimeStats, len(rt.byNode))
	for k, v := range rt.byNode {
		out[k] = *v
	}
	return out
}

func (rt *Runtime) Stats(nodeID string) (NodeRuntimeStats, bool) {
	if rt == nil || nodeID == "" {
		return NodeRuntimeStats{}, false
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st, ok := rt.byNode[nodeID]
	if !ok {
		return NodeRuntimeStats{}, false
	}
	return *st, true
}
