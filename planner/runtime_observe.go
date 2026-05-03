package planner

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cevazrem/gql-query-planner/planner/estimate"
	"github.com/cevazrem/gql-query-planner/planner/execute"
	"github.com/cevazrem/gql-query-planner/planner/logical"
	"github.com/cevazrem/gql-query-planner/planner/physical"
	"github.com/cevazrem/gql-query-planner/planner/stats"
)

type executionSummary struct {
	PredictedCumulativeCost float64
	PredictedRootMaxCost    float64
	ActualLogicalCalls      int64
	ActualPhysicalCalls     int64
	ActualRowsOut           int64
	ActualResolverNanos     int64
	ActualWallNanos         int64
	ActualMaxParallelism    int64
	ActualCacheLookups      int64
	ActualCacheHits         int64
	ActualCacheMisses       int64
	ActualCacheWaits        int64
}

func (e *Planner) recordRuntimeStats(
	annotated *estimate.AnnotatedPlan,
	phys *physical.Plan,
	rt *execute.Runtime,
	actualWall time.Duration,
) executionSummary {
	if e == nil || e.stats == nil || annotated == nil || annotated.Plan == nil || annotated.Plan.Root == nil || rt == nil || phys == nil {
		return executionSummary{}
	}

	summary := executionSummary{
		ActualWallNanos: int64(actualWall),
	}

	for _, ch := range annotated.Plan.Root.Children {
		if strat, ok := phys.Strategies[ch.ID]; ok && strat.Cost.TotalCost > summary.PredictedRootMaxCost {
			summary.PredictedRootMaxCost = strat.Cost.TotalCost
		}
		e.recordRuntimeStatsNode(ch, 1, annotated, phys, rt, &summary)
	}

	return summary
}

func (e *Planner) recordRuntimeStatsNode(
	node *logical.Node,
	parentRowsForKey float64,
	annotated *estimate.AnnotatedPlan,
	phys *physical.Plan,
	rt *execute.Runtime,
	summary *executionSummary,
) {
	if node == nil {
		return
	}

	ann, hasAnn := annotated.Annotations[node.ID]
	if strat, ok := phys.Strategies[node.ID]; ok {
		summary.PredictedCumulativeCost += strat.Cost.TotalCost
	} else if hasAnn {
		// fallback на случай, если для узла по какой-то причине нет физической стратегии
		summary.PredictedCumulativeCost += ann.Cost.TotalCost
	}

	st, hasStats := rt.Stats(node.ID)
	if hasStats {
		summary.ActualLogicalCalls += st.ActualCalls
		summary.ActualPhysicalCalls += st.ActualPhysicalCalls
		summary.ActualRowsOut += st.ActualRowsOut
		summary.ActualResolverNanos += st.TotalResolverNanos
		summary.ActualCacheLookups += st.CacheLookups
		summary.ActualCacheHits += st.CacheHits
		summary.ActualCacheMisses += st.CacheMisses
		summary.ActualCacheWaits += st.CacheWaits
		if st.MaxObservedParallelism > summary.ActualMaxParallelism {
			summary.ActualMaxParallelism = st.MaxObservedParallelism
		}
	}

	// ВАЖНО: именно этот блок пишет runtime-статистику /query2 в общий stats store.
	if hasAnn && hasStats && st.ActualPhysicalCalls > 0 {
		observedLen := -1
		if node.IsList && st.ActualCalls > 0 {
			observedLen = int(math.Round(float64(st.ActualRowsOut) / float64(st.ActualCalls)))
			if observedLen < 0 {
				observedLen = 0
			}
		}

		latencyMsPerPhysicalCall := float64(st.TotalResolverNanos) / float64(time.Millisecond)
		latencyMsPerPhysicalCall /= float64(st.ActualPhysicalCalls)

		widthBytes := -1.0
		if st.ObservedWidthBytes > 0 {
			denom := float64(st.ActualRowsOut)
			if denom <= 0 {
				denom = float64(st.ActualPhysicalCalls)
			}
			if denom <= 0 {
				denom = 1
			}
			widthBytes = float64(st.ObservedWidthBytes) / denom
		}

		selectedChildren := childResponseKeys(node)
		e.stats.Record(stats.Observation{
			Key: stats.FieldKey{
				ResolverKey:             node.ResolverKey(),
				DepthBucket:             depthBucket(node.Depth),
				ArgShape:                node.ArgShape,
				SelectedChildrenShape:   strings.Join(selectedChildren, ","),
				ParentCardinalityBucket: cardinalityBucket(parentRowsForKey),
			},
			ObservedLen:      observedLen,
			LatencyMs:        latencyMsPerPhysicalCall,
			WidthBytes:       widthBytes,
			HadError:         false,
			SelectedChildren: selectedChildren,
			ObservedAt:       time.Now(),
		})
	}

	nextParentRows := parentRowsForKey
	if hasAnn {
		nextParentRows = ann.Card.RowsOutTotal
	}
	if hasStats && st.ActualRowsOut > 0 {
		nextParentRows = float64(st.ActualRowsOut)
	}

	for _, ch := range node.Children {
		e.recordRuntimeStatsNode(ch, nextParentRows, annotated, phys, rt, summary)
	}
}

func formatExecutionSummaryLines(summary executionSummary) string {
	return strings.Join([]string{
		"predicted_cumulative_total=" + strconv.FormatFloat(summary.PredictedCumulativeCost, 'f', 2, 64),
		"predicted_root_max_total=" + strconv.FormatFloat(summary.PredictedRootMaxCost, 'f', 2, 64),
		"actual_logical_calls=" + strconv.FormatInt(summary.ActualLogicalCalls, 10),
		"actual_physical_calls=" + strconv.FormatInt(summary.ActualPhysicalCalls, 10),
		"actual_rows_out=" + strconv.FormatInt(summary.ActualRowsOut, 10),
		"actual_cumulative_resolver_ms=" + strconv.FormatFloat(float64(summary.ActualResolverNanos)/float64(time.Millisecond), 'f', 2, 64),
		"actual_wall_ms=" + strconv.FormatFloat(float64(summary.ActualWallNanos)/float64(time.Millisecond), 'f', 2, 64),
		"actual_max_parallelism=" + strconv.FormatInt(summary.ActualMaxParallelism, 10),
		"actual_cache_lookups=" + strconv.FormatInt(summary.ActualCacheLookups, 10),
		"actual_cache_hits=" + strconv.FormatInt(summary.ActualCacheHits, 10),
		"actual_cache_misses=" + strconv.FormatInt(summary.ActualCacheMisses, 10),
		"actual_cache_waits=" + strconv.FormatInt(summary.ActualCacheWaits, 10),
	}, " ")
}
