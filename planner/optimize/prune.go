package optimize

import (
	"math"
	"sort"

	"github.com/cevazrem/gql-query-planner/planner/physical"
)

func dominates(a, b *physical.CandidatePath) bool {
	if a == nil || b == nil {
		return false
	}

	notWorse :=
		a.Cost.Total <= b.Cost.Total &&
			a.Resources.PeakInflight <= b.Resources.PeakInflight &&
			a.Resources.PeakMemory <= b.Resources.PeakMemory &&
			a.Resources.DescendantConcurrency <= b.Resources.DescendantConcurrency

	strictlyBetter :=
		a.Cost.Total < b.Cost.Total ||
			a.Resources.PeakInflight < b.Resources.PeakInflight ||
			a.Resources.PeakMemory < b.Resources.PeakMemory ||
			a.Resources.DescendantConcurrency < b.Resources.DescendantConcurrency

	return notWorse && strictlyBetter
}

func pruneDominated(in []*physical.CandidatePath) []*physical.CandidatePath {
	out := make([]*physical.CandidatePath, 0, len(in))
	for i, c := range in {
		dominated := false
		for j, other := range in {
			if i == j {
				continue
			}
			if dominates(other, c) {
				dominated = true
				break
			}
		}
		if !dominated {
			out = append(out, c)
		}
	}
	return out
}

func keepTopK(in []*physical.CandidatePath, k int) []*physical.CandidatePath {
	sort.Slice(in, func(i, j int) bool {
		return betterCandidate(in[i], in[j])
	})
	if k <= 0 || len(in) <= k {
		return in
	}
	return in[:k]
}

func betterCandidate(a, b *physical.CandidatePath) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	if !almostEqual(a.Cost.Total, b.Cost.Total) {
		return a.Cost.Total < b.Cost.Total
	}
	if !almostEqual(a.Card.PhysicalCalls, b.Card.PhysicalCalls) {
		return a.Card.PhysicalCalls < b.Card.PhysicalCalls
	}
	if !almostEqual(a.Resources.PeakInflight, b.Resources.PeakInflight) {
		return a.Resources.PeakInflight < b.Resources.PeakInflight
	}
	if !almostEqual(a.Resources.DescendantConcurrency, b.Resources.DescendantConcurrency) {
		return a.Resources.DescendantConcurrency < b.Resources.DescendantConcurrency
	}
	if a.Strategy.ListMode == physical.ListBatched && b.Strategy.ListMode == physical.ListBatched && a.Strategy.BatchSize != b.Strategy.BatchSize {
		return a.Strategy.BatchSize > b.Strategy.BatchSize
	}
	if a.PathLabel != b.PathLabel {
		return a.PathLabel < b.PathLabel
	}
	return a.NodeID < b.NodeID
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
