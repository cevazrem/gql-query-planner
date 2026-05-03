package optimize

import (
	"fmt"

	"github.com/cevazrem/gql-query-planner/planner/estimate"
	"github.com/cevazrem/gql-query-planner/planner/physical"
)

type Config struct {
	RootWorkers        int
	MaxInFlight        int
	HeavyNodeCost      float64
	SlowLatencyCost    float64
	ParallelizeMinRows float64
	BatchMinRows       float64
	DefaultBatchSize   int
}

func DefaultConfig() Config {
	return Config{RootWorkers: 16, MaxInFlight: 32, HeavyNodeCost: 100, SlowLatencyCost: 25, ParallelizeMinRows: 4, BatchMinRows: 16, DefaultBatchSize: 100}
}

func Choose(annotated *estimate.AnnotatedPlan, cfg Config) *physical.Plan {
	out := &physical.Plan{RootWorkers: cfg.RootWorkers, MaxInFlight: cfg.MaxInFlight, Strategies: map[string]physical.NodeStrategy{}}
	if annotated == nil || annotated.Plan == nil || annotated.Plan.Root == nil {
		return out
	}

	searchCfg := DefaultSearchConfig()
	searchCfg.MaxInflight = float64(cfg.MaxInFlight)
	candidates := make([]*physical.CandidatePath, 0, len(annotated.Plan.Root.Children))
	for _, ch := range annotated.Plan.Root.Children {
		cands := enumerateSubtree(ch, annotated.Annotations, cfg, searchCfg)
		if len(cands) == 0 {
			continue
		}
		candidates = append(candidates, cands[0])
	}

	if len(candidates) == 0 {
		return out
	}

	for _, cand := range candidates {
		materializeCandidate(cand, out)
	}
	return out
}

func materializeCandidate(c *physical.CandidatePath, out *physical.Plan) {
	if c == nil || out == nil {
		return
	}

	strat := c.Strategy
	strat.PathLabel = c.PathLabel
	strat.ConsideredPaths = c.ConsideredPaths
	strat.CandidateTrace = make([]physical.CandidateTrace, len(c.Trace))
	for i, tr := range c.Trace {
		strat.CandidateTrace[i] = physical.CandidateTrace{
			PathLabel:     tr.PathLabel,
			TotalCost:     tr.TotalCost,
			PhysicalCalls: tr.PhysicalCalls,
			PeakInflight:  tr.PeakInflight,
			PeakMemory:    tr.PeakMemory,
			BatchSize:     tr.BatchSize,
			Workers:       tr.Workers,
			FieldsMode:    tr.FieldsMode,
			ListMode:      tr.ListMode,
		}
	}
	strat.PeakInflight = c.Resources.PeakInflight
	strat.PeakMemory = c.Resources.PeakMemory
	strat.Card = estimate.CardinalityEstimate{
		CallsPerParent:    1,
		RowsOutPerParent:  c.Card.ExpectedRows,
		UpperBoundRows:    c.Card.UpperBoundRows,
		ParentRows:        c.Card.ParentRows,
		LogicalCalls:      c.Card.LogicalCalls,
		PhysicalCalls:     c.Card.PhysicalCalls,
		TotalCalls:        c.Card.LogicalCalls,
		RowsOutTotal:      c.Card.RowsOutTotal,
		RowConfidence:     c.Risk.RowConfidence,
		LatencyConfidence: c.Risk.LatencyConfidence,
		Confidence:        minFloat(c.Risk.RowConfidence, c.Risk.LatencyConfidence),
		Source:            estimate.Source(c.Card.Source),
		UpperBoundSource:  estimate.Source(c.Card.UpperSource),
	}
	strat.Cost = estimate.CostEstimate{
		StartupCost:       c.Cost.Startup,
		SelfCost:          c.Cost.Self,
		ChildrenCost:      c.Cost.Children,
		TotalCost:         c.Cost.Total,
		WidthBytes:        c.Cost.Width,
		Confidence:        minFloat(c.Risk.RowConfidence, c.Risk.LatencyConfidence),
		RowConfidence:     c.Risk.RowConfidence,
		LatencyConfidence: c.Risk.LatencyConfidence,
	}

	out.Strategies[c.NodeID] = strat

	for _, ch := range c.Children {
		materializeCandidate(ch, out)
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func MustChoose(annotated *estimate.AnnotatedPlan, cfg Config) (*physical.Plan, error) {
	if annotated == nil || annotated.Plan == nil || annotated.Plan.Root == nil {
		return nil, fmt.Errorf("optimize: nil plan")
	}
	return Choose(annotated, cfg), nil
}
