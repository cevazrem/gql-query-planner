package optimize

import (
	"fmt"
	"math"

	"github.com/cevazrem/gql-query-planner/planner/estimate"
	"github.com/cevazrem/gql-query-planner/planner/logical"
	"github.com/cevazrem/gql-query-planner/planner/physical"
)

type SearchConfig struct {
	MaxInflight              float64
	MaxMemory                float64
	MaxDescendantConcurrency float64
	KeepTopK                 int
}

func DefaultSearchConfig() SearchConfig {
	return SearchConfig{
		MaxInflight:              32,
		MaxMemory:                256 * 1024 * 1024,
		MaxDescendantConcurrency: 4,
		KeepTopK:                 8,
	}
}

func enumerateLocalCandidates(node *logical.Node, ann estimate.NodeAnnotation, cfg Config) []*physical.CandidatePath {
	if node == nil {
		return nil
	}

	if !node.IsList && len(node.Children) == 0 {
		out := []*physical.CandidatePath{makeScalarCandidate(node, ann)}
		for _, c := range out {
			c.ConsideredPaths = len(out)
		}
		return out
	}

	if !node.IsList {
		seqWorkers := 1
		parWorkers := minInt(cfg.RootWorkers, 4)
		if parWorkers <= 0 {
			parWorkers = 4
		}
		out := []*physical.CandidatePath{
			makeObjectCandidate(node, ann, physical.NodeStrategy{
				FieldsMode: physical.FieldsSequential,
				Workers:    seqWorkers,
				UseCache:   node.Meta.Caps.Cacheable,
				Reason:     "object seq",
			}, "object_seq"),
			makeObjectCandidate(node, ann, physical.NodeStrategy{
				FieldsMode: physical.FieldsParallel,
				Workers:    parWorkers,
				UseCache:   node.Meta.Caps.Cacheable,
				Reason:     fmt.Sprintf("object par(%d)", parWorkers),
			}, "object_par"),
		}
		for _, c := range out {
			c.ConsideredPaths = len(out)
		}
		return out
	}

	parallel2 := minInt(cfg.RootWorkers, 2)
	if parallel2 <= 0 {
		parallel2 = 2
	}
	parallel4 := minInt(cfg.RootWorkers, 4)
	if parallel4 <= 0 {
		parallel4 = 4
	}
	parallel16 := minInt(cfg.RootWorkers, 16)
	if parallel16 <= 0 {
		parallel16 = 16
	}

	hasNestedLists := hasNestedListDescendant(node)
	tier := conservativeListTier(node, ann)
	candidates := []*physical.CandidatePath{
		makeListCandidate(node, ann, physical.NodeStrategy{
			ListMode: physical.ListSerial,
			Workers:  1,
			UseCache: node.Meta.Caps.Cacheable,
			Reason:   "list serial",
		}, "list_serial"),
	}
	switch tier {
	case conservativeTierNormal:
		candidates = append(candidates,
			makeListCandidate(node, ann, physical.NodeStrategy{
				ListMode: physical.ListParallel,
				Workers:  parallel2,
				UseCache: node.Meta.Caps.Cacheable,
				Reason:   fmt.Sprintf("list parallel(%d)", parallel2),
			}, "list_parallel_2"),
			makeListCandidate(node, ann, physical.NodeStrategy{
				ListMode: physical.ListParallel,
				Workers:  parallel4,
				UseCache: node.Meta.Caps.Cacheable,
				Reason:   fmt.Sprintf("list parallel(%d)", parallel4),
			}, "list_parallel_4"),
		)
		if !hasNestedLists {
			candidates = append(candidates, makeListCandidate(node, ann, physical.NodeStrategy{
				ListMode: physical.ListParallel,
				Workers:  parallel16,
				UseCache: node.Meta.Caps.Cacheable,
				Reason:   fmt.Sprintf("list parallel(%d)", parallel16),
			}, "list_parallel_16"))
		}
	case conservativeTierLimited:
		candidates = append(candidates,
			makeListCandidate(node, ann, physical.NodeStrategy{
				ListMode: physical.ListParallel,
				Workers:  parallel2,
				UseCache: node.Meta.Caps.Cacheable,
				Reason:   fmt.Sprintf("list parallel(%d)", parallel2),
			}, "list_parallel_2"),
		)
	}

	if node.Meta.Caps.Batchable {
		for _, bs := range batchSizes(node.Meta.Caps.MaxBatchSize) {
			candidates = append(candidates, makeListCandidate(node, ann, physical.NodeStrategy{
				ListMode:             physical.ListBatched,
				UseBatching:          true,
				BatchSize:            bs,
				MaxConcurrentBatches: minInt(cfg.RootWorkers, 2),
				Workers:              parallel16,
				UseCache:             node.Meta.Caps.Cacheable,
				Reason:               fmt.Sprintf("list batched(%d)", bs),
			}, fmt.Sprintf("list_batched_%d", bs)))
		}
	}

	for _, c := range candidates {
		c.ConsideredPaths = len(candidates)
	}
	return candidates
}

func makeScalarCandidate(node *logical.Node, ann estimate.NodeAnnotation) *physical.CandidatePath {
	return &physical.CandidatePath{
		NodeID:    node.ID,
		PathLabel: "scalar_default",
		Strategy: physical.NodeStrategy{
			NodeID:   node.ID,
			Workers:  1,
			UseCache: node.Meta.Caps.Cacheable,
			Reason:   "scalar default",
		},
		Card: physical.CandidateCardinality{
			ExpectedRows:   ann.Card.RowsOutPerParent,
			UpperBoundRows: ann.Card.UpperBoundRows,
			Source:         string(ann.Card.Source),
			UpperSource:    string(ann.Card.UpperBoundSource),
			ParentRows:     ann.Card.ParentRows,
			LogicalCalls:   ann.Card.LogicalCalls,
			PhysicalCalls:  ann.Card.PhysicalCalls,
			RowsOutTotal:   ann.Card.RowsOutTotal,
		},
		Cost: physical.CandidateCost{
			Startup: ann.Cost.StartupCost,
			Self:    ann.Cost.SelfCost,
			Total:   ann.Cost.TotalCost,
			Width:   ann.Cost.WidthBytes,
		},
		Resources: physical.CandidateResources{
			PeakInflight:          1,
			PeakMemory:            ann.Card.RowsOutTotal * ann.Cost.WidthBytes,
			DescendantConcurrency: 1,
		},
		Risk: physical.CandidateRisk{
			RowConfidence:     ann.Card.RowConfidence,
			LatencyConfidence: ann.Cost.LatencyConfidence,
		},
	}
}

func makeObjectCandidate(node *logical.Node, ann estimate.NodeAnnotation, s physical.NodeStrategy, label string) *physical.CandidatePath {
	peakInflight := 1.0
	if s.FieldsMode == physical.FieldsParallel {
		peakInflight = math.Max(1, float64(maxInt(1, s.Workers)))
	}

	startup := ann.Cost.StartupCost
	self := ann.Cost.SelfCost
	if s.FieldsMode == physical.FieldsParallel {
		self += peakInflight * 0.15
	}

	return &physical.CandidatePath{
		NodeID:    node.ID,
		PathLabel: label,
		Strategy:  withNodeID(node.ID, s),
		Card: physical.CandidateCardinality{
			ExpectedRows:   ann.Card.RowsOutPerParent,
			UpperBoundRows: ann.Card.UpperBoundRows,
			Source:         string(ann.Card.Source),
			UpperSource:    string(ann.Card.UpperBoundSource),
			ParentRows:     ann.Card.ParentRows,
			LogicalCalls:   ann.Card.LogicalCalls,
			PhysicalCalls:  ann.Card.PhysicalCalls,
			RowsOutTotal:   ann.Card.RowsOutTotal,
		},
		Cost: physical.CandidateCost{
			Startup: startup,
			Self:    self,
			Total:   startup + self,
			Width:   ann.Cost.WidthBytes,
		},
		Resources: physical.CandidateResources{
			PeakInflight:          peakInflight,
			PeakMemory:            ann.Card.RowsOutTotal * ann.Cost.WidthBytes,
			DescendantConcurrency: 1,
		},
		Risk: physical.CandidateRisk{
			RowConfidence:     ann.Card.RowConfidence,
			LatencyConfidence: ann.Cost.LatencyConfidence,
		},
	}
}

func makeListCandidate(node *logical.Node, ann estimate.NodeAnnotation, s physical.NodeStrategy, label string) *physical.CandidatePath {
	logicalCalls := math.Max(1, ann.Card.ParentRows*ann.Card.CallsPerParent)
	rowsOutTotal := logicalCalls * ann.Card.RowsOutPerParent
	physicalCalls := logicalCalls

	if s.ListMode == physical.ListBatched {
		batchSize := maxInt(1, s.BatchSize)
		physicalCalls = math.Ceil(logicalCalls / float64(batchSize))
		s.BatchSize = batchSize
		if s.MaxConcurrentBatches <= 0 {
			s.MaxConcurrentBatches = maxInt(1, minInt(s.Workers, int(physicalCalls)))
		}
	}

	startupPerInvocation := ann.Cost.StartupPerCall
	selfPerLogicalCall := ann.Cost.SelfPerCall
	startupTotal := startupPerInvocation * physicalCalls
	selfTotal := selfPerLogicalCall * logicalCalls

	peakInflight := 1.0
	coordPenalty := 0.0
	switch s.ListMode {
	case physical.ListSerial:
		peakInflight = 1
	case physical.ListParallel:
		peakInflight = math.Min(physicalCalls, float64(maxInt(1, s.Workers)))
		coordPenalty = peakInflight * 0.5
	case physical.ListBatched:
		peakInflight = math.Min(physicalCalls, float64(maxInt(1, s.MaxConcurrentBatches)))
		coordPenalty = peakInflight * 0.25
	}

	selfTotal += coordPenalty

	return &physical.CandidatePath{
		NodeID:    node.ID,
		PathLabel: label,
		Strategy:  withNodeID(node.ID, s),
		Card: physical.CandidateCardinality{
			ExpectedRows:   ann.Card.RowsOutPerParent,
			UpperBoundRows: ann.Card.UpperBoundRows,
			Source:         string(ann.Card.Source),
			UpperSource:    string(ann.Card.UpperBoundSource),
			ParentRows:     ann.Card.ParentRows,
			LogicalCalls:   logicalCalls,
			PhysicalCalls:  physicalCalls,
			RowsOutTotal:   rowsOutTotal,
		},
		Cost: physical.CandidateCost{
			Startup: startupTotal,
			Self:    selfTotal,
			Total:   startupTotal + selfTotal,
			Width:   ann.Cost.WidthBytes,
		},
		Resources: physical.CandidateResources{
			PeakInflight:          peakInflight,
			PeakMemory:            rowsOutTotal * ann.Cost.WidthBytes,
			DescendantConcurrency: 1,
		},
		Risk: physical.CandidateRisk{
			RowConfidence:     ann.Card.RowConfidence,
			LatencyConfidence: ann.Cost.LatencyConfidence,
		},
	}
}

type conservativeTier int

const (
	conservativeTierNormal conservativeTier = iota
	conservativeTierLimited
	conservativeTierStrict
)

func conservativeListTier(node *logical.Node, ann estimate.NodeAnnotation) conservativeTier {
	conf := minFloat(ann.Card.RowConfidence, ann.Cost.LatencyConfidence)
	hasNestedLists := hasNestedListDescendant(node)

	if isLowConfidenceUpperBoundAnnotation(ann) {
		if ann.Card.LogicalCalls >= 1000 || ann.Card.RowsOutTotal >= 100000 || ann.Card.ParentRows >= 1000 || hasNestedLists {
			return conservativeTierStrict
		}
		return conservativeTierLimited
	}

	if hasNestedLists {
		switch {
		case conf < 0.35:
			return conservativeTierStrict
		case conf < 0.55:
			return conservativeTierLimited
		default:
			return conservativeTierNormal
		}
	}

	if conf < 0.30 && ann.Card.RowsOutTotal >= 10000 {
		return conservativeTierLimited
	}
	return conservativeTierNormal
}

func isLowConfidenceUpperBoundAnnotation(ann estimate.NodeAnnotation) bool {
	if ann.Card.RowConfidence > 0.45 && ann.Cost.LatencyConfidence > 0.45 {
		return false
	}
	switch ann.Card.Source {
	case estimate.SourceArgUpperBound, estimate.SourceInheritedParentBound:
		return true
	}
	switch ann.Card.UpperBoundSource {
	case estimate.SourceArgUpperBound, estimate.SourceInheritedParentBound:
		return true
	}
	return false
}

func hasNestedListDescendant(node *logical.Node) bool {
	if node == nil {
		return false
	}
	for _, ch := range node.Children {
		if ch == nil {
			continue
		}
		if ch.IsList || hasAnyListBelow(ch) {
			return true
		}
	}
	return false
}

func hasAnyListBelow(node *logical.Node) bool {
	if node == nil {
		return false
	}
	for _, ch := range node.Children {
		if ch == nil {
			continue
		}
		if ch.IsList || hasAnyListBelow(ch) {
			return true
		}
	}
	return false
}

func withNodeID(id string, s physical.NodeStrategy) physical.NodeStrategy {
	s.NodeID = id
	return s
}

func minInt(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func batchSizes(maxSize int) []int {
	if maxSize <= 0 {
		maxSize = 100
	}
	half := maxInt(1, maxSize/2)
	if half == maxSize {
		return []int{maxSize}
	}
	return []int{half, maxSize}
}
