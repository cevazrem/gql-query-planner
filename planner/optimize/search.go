package optimize

import (
	"sort"

	"github.com/cevazrem/gql-query-planner/planner/estimate"
	"github.com/cevazrem/gql-query-planner/planner/logical"
	"github.com/cevazrem/gql-query-planner/planner/physical"
)

func enumerateSubtree(node *logical.Node, annotations map[string]estimate.NodeAnnotation, cfg Config, searchCfg SearchConfig) []*physical.CandidatePath {
	if node == nil {
		return nil
	}

	ann := annotations[node.ID]
	local := enumerateLocalCandidates(node, ann, cfg)
	if len(node.Children) == 0 {
		local = attachTrace(local)
		return keepTopK(pruneDominated(local), searchCfg.KeepTopK)
	}

	childSets := make([][]*physical.CandidatePath, 0, len(node.Children))
	for _, ch := range node.Children {
		childSets = append(childSets, enumerateSubtree(ch, annotations, cfg, searchCfg))
	}

	out := make([]*physical.CandidatePath, 0, len(local))
	childCombos := chooseChildCombinations(childSets, childCombinationLimit(searchCfg))
	for _, parentCand := range local {
		for _, children := range childCombos {
			parentCopy := *parentCand
			parentCopy.Children = children

			childrenCost, peakInflight, peakMemory, descendantConcurrency := combineChildExecution(parentCand, children)
			parentCopy.Cost.Children = childrenCost
			parentCopy.Cost.Total = parentCopy.Cost.Startup + parentCopy.Cost.Self + parentCopy.Cost.Children
			parentCopy.Resources.PeakInflight = peakInflight
			parentCopy.Resources.PeakMemory = peakMemory
			parentCopy.Resources.DescendantConcurrency = descendantConcurrency

			out = append(out, &parentCopy)
		}
	}

	out = attachTrace(out)
	out = filterByBudget(out, searchCfg)
	out = pruneDominated(out)
	out = keepTopK(out, searchCfg.KeepTopK)
	return out
}

func combineChildExecution(parent *physical.CandidatePath, children []*physical.CandidatePath) (childrenCost, peakInflight, peakMemory, descendantConcurrency float64) {
	peakInflight = 1
	peakMemory = 0
	if parent != nil {
		peakInflight = parent.Resources.PeakInflight
		peakMemory = parent.Resources.PeakMemory
		descendantConcurrency = maxFloat(1, parent.Resources.DescendantConcurrency)
	}
	if descendantConcurrency <= 0 {
		descendantConcurrency = 1
	}
	if len(children) == 0 {
		return 0, peakInflight, peakMemory, descendantConcurrency
	}

	childMaxCost := 0.0
	childSumCost := 0.0
	childMaxInflight := 1.0
	childMaxMemory := 0.0
	childSumMemory := 0.0
	childMaxDescConcurrency := 1.0
	for _, ch := range children {
		if ch == nil {
			continue
		}
		childSumCost += ch.Cost.Total
		if ch.Cost.Total > childMaxCost {
			childMaxCost = ch.Cost.Total
		}
		if ch.Resources.PeakInflight > childMaxInflight {
			childMaxInflight = ch.Resources.PeakInflight
		}
		if ch.Resources.PeakMemory > childMaxMemory {
			childMaxMemory = ch.Resources.PeakMemory
		}
		if ch.Resources.DescendantConcurrency > childMaxDescConcurrency {
			childMaxDescConcurrency = ch.Resources.DescendantConcurrency
		}
		childSumMemory += ch.Resources.PeakMemory
	}

	effectiveWorkers := 1.0
	if parent != nil && parent.Strategy.Workers > 1 {
		effectiveWorkers = float64(parent.Strategy.Workers)
	}

	switch {
	case parent != nil && parent.Strategy.FieldsMode == physical.FieldsParallel:
		coordPenalty := 0.20 * float64(len(children))
		childrenCost = childMaxCost + coordPenalty
		peakInflight = maxFloat(peakInflight, effectiveWorkers*childMaxInflight)
		peakMemory += childSumMemory
		descendantConcurrency = maxFloat(descendantConcurrency, childMaxDescConcurrency)
	case parent != nil && parent.Strategy.ListMode == physical.ListParallel:
		parallelWorkers := effectiveWorkers
		if conservativeParallelPenalty(parent) {
			parallelWorkers = 1
		}
		coordPenalty := 0.10*effectiveWorkers*float64(len(children)) + conservativeCoordPenalty(parent, effectiveWorkers)
		saturationPenalty := descendantSaturationPenalty(parent, children, effectiveWorkers)
		childrenCost = (childSumCost / parallelWorkers) + coordPenalty + saturationPenalty
		peakInflight = maxFloat(peakInflight, effectiveWorkers*childMaxInflight)
		peakMemory += childMaxMemory
		descendantConcurrency = maxFloat(descendantConcurrency, effectiveWorkers*childMaxDescConcurrency)
	case parent != nil && parent.Strategy.ListMode == physical.ListBatched:
		batchConcurrency := maxFloat(1, float64(maxInt(1, parent.Strategy.MaxConcurrentBatches)))
		coordPenalty := 0.05*effectiveWorkers*float64(len(children)) + 0.10*batchConcurrency + conservativeCoordPenalty(parent, batchConcurrency)
		childrenCost = (childSumCost / effectiveWorkers) + coordPenalty

		nonBatchedChildMaxInflight := 1.0
		batchedChildMaxInflight := 1.0
		for _, ch := range children {
			if ch == nil {
				continue
			}
			if ch.Strategy.ListMode == physical.ListBatched {
				if ch.Resources.PeakInflight > batchedChildMaxInflight {
					batchedChildMaxInflight = ch.Resources.PeakInflight
				}
				continue
			}
			if ch.Resources.PeakInflight > nonBatchedChildMaxInflight {
				nonBatchedChildMaxInflight = ch.Resources.PeakInflight
			}
		}

		peakInflight = maxFloat(peakInflight, batchConcurrency)
		peakInflight = maxFloat(peakInflight, effectiveWorkers*nonBatchedChildMaxInflight)
		peakInflight = maxFloat(peakInflight, batchedChildMaxInflight)
		peakMemory += childMaxMemory
		descendantConcurrency = maxFloat(descendantConcurrency, batchConcurrency*childMaxDescConcurrency)
	default:
		childrenCost = childSumCost
		peakInflight = maxFloat(peakInflight, childMaxInflight)
		peakMemory += childSumMemory
		if parent != nil {
			descendantConcurrency = maxFloat(descendantConcurrency, childMaxDescConcurrency)
		}
	}

	return childrenCost, peakInflight, peakMemory, descendantConcurrency
}

func childCombinationLimit(cfg SearchConfig) int {
	limit := maxInt(1, cfg.KeepTopK)
	if limit < 8 {
		limit = 8
	}
	if limit > 64 {
		limit = 64
	}
	return limit
}

func chooseChildCombinations(sets [][]*physical.CandidatePath, limit int) [][]*physical.CandidatePath {
	if len(sets) == 0 {
		return [][]*physical.CandidatePath{{}}
	}
	limit = maxInt(1, limit)
	out := make([][]*physical.CandidatePath, 0, limit)
	current := make([]*physical.CandidatePath, 0, len(sets))
	var walk func(int)
	walk = func(idx int) {
		if len(out) >= limit {
			return
		}
		if idx == len(sets) {
			combo := append([]*physical.CandidatePath(nil), current...)
			out = append(out, combo)
			return
		}
		set := sets[idx]
		if len(set) == 0 {
			walk(idx + 1)
			return
		}
		for _, cand := range set {
			current = append(current, cand)
			walk(idx + 1)
			current = current[:len(current)-1]
			if len(out) >= limit {
				return
			}
		}
	}
	walk(0)
	if len(out) == 0 {
		return [][]*physical.CandidatePath{{}}
	}
	return out
}

func conservativeParallelPenalty(parent *physical.CandidatePath) bool {
	if parent == nil {
		return false
	}
	return parent.Risk.RowConfidence <= 0.30 &&
		(parent.Card.Source == string(estimate.SourceArgUpperBound) ||
			parent.Card.Source == string(estimate.SourceInheritedParentBound) ||
			parent.Card.UpperSource == string(estimate.SourceArgUpperBound) ||
			parent.Card.UpperSource == string(estimate.SourceInheritedParentBound))
}

func conservativeCoordPenalty(parent *physical.CandidatePath, concurrency float64) float64 {
	if !conservativeParallelPenalty(parent) {
		return 0
	}
	rows := maxFloat(1, parent.Card.ExpectedRows)
	return 0.50 * concurrency * rows
}

func descendantSaturationPenalty(parent *physical.CandidatePath, children []*physical.CandidatePath, upstreamConcurrency float64) float64 {
	if parent == nil || upstreamConcurrency <= 1 {
		return 0
	}
	penalty := 0.0
	for _, ch := range children {
		if ch == nil {
			continue
		}
		descendantConcurrency := maxFloat(1, ch.Resources.DescendantConcurrency)
		effectiveDescConcurrency := upstreamConcurrency * descendantConcurrency
		safeConcurrency := safeDescendantConcurrency(parent, ch)
		if effectiveDescConcurrency <= safeConcurrency {
			continue
		}
		overload := (effectiveDescConcurrency / safeConcurrency) - 1
		base := maxFloat(ch.Cost.Total*0.15, ch.Cost.Children*0.10)
		base = maxFloat(base, ch.Cost.Self)
		confidence := minFloat(ch.Risk.RowConfidence, ch.Risk.LatencyConfidence)
		confidenceScale := 1 + (1 - confidence)
		penalty += overload * base * confidenceScale
	}
	return penalty
}

func safeDescendantConcurrency(parent, child *physical.CandidatePath) float64 {
	safe := 4.0
	if parent != nil && conservativeParallelPenalty(parent) {
		safe = 2.0
	}
	if child == nil {
		return safe
	}
	confidence := minFloat(child.Risk.RowConfidence, child.Risk.LatencyConfidence)
	switch {
	case confidence >= 0.75:
		safe = maxFloat(safe, 8.0)
	case confidence >= 0.55:
		safe = maxFloat(safe, 4.0)
	default:
		safe = 2.0
	}
	if child.Strategy.ListMode == physical.ListParallel {
		safe = minFloat(safe, 4.0)
	}
	return safe
}

func filterByBudget(in []*physical.CandidatePath, cfg SearchConfig) []*physical.CandidatePath {
	out := make([]*physical.CandidatePath, 0, len(in))
	for _, c := range in {
		if cfg.MaxDescendantConcurrency > 0 && c.Resources.DescendantConcurrency > cfg.MaxDescendantConcurrency {
			continue
		}
		if cfg.MaxInflight > 0 && c.Resources.PeakInflight > cfg.MaxInflight {
			continue
		}
		if cfg.MaxMemory > 0 && c.Resources.PeakMemory > cfg.MaxMemory {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return in
	}
	return out
}

func attachTrace(in []*physical.CandidatePath) []*physical.CandidatePath {
	if len(in) == 0 {
		return in
	}
	ordered := append([]*physical.CandidatePath(nil), in...)
	sort.Slice(ordered, func(i, j int) bool {
		return betterCandidate(ordered[i], ordered[j])
	})
	trace := make([]physical.CandidateTrace, 0, len(ordered))
	for _, c := range ordered {
		trace = append(trace, physical.CandidateTrace{
			PathLabel:     c.PathLabel,
			TotalCost:     c.Cost.Total,
			PhysicalCalls: c.Card.PhysicalCalls,
			PeakInflight:  c.Resources.PeakInflight,
			PeakMemory:    c.Resources.PeakMemory,
			BatchSize:     c.Strategy.BatchSize,
			Workers:       c.Strategy.Workers,
			FieldsMode:    c.Strategy.FieldsMode,
			ListMode:      c.Strategy.ListMode,
		})
	}
	for _, c := range in {
		c.Trace = append([]physical.CandidateTrace(nil), trace...)
		c.ConsideredPaths = len(trace)
	}
	return in
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
