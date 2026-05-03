package explain

import (
	"fmt"
	"strings"

	"github.com/cevazrem/gql-query-planner/planner/estimate"
	"github.com/cevazrem/gql-query-planner/planner/logical"
	"github.com/cevazrem/gql-query-planner/planner/physical"
)

func Text(annotated *estimate.AnnotatedPlan, plan *physical.Plan) string {
	if annotated == nil || annotated.Plan == nil || annotated.Plan.Root == nil {
		return "<nil plan>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "QUERY PLAN\n")
	fmt.Fprintf(&b, "  operation=%s type=%s fingerprint=%s\n", annotated.Plan.OperationName, annotated.Plan.OperationType, annotated.Plan.QueryFingerprint)
	fmt.Fprintf(&b, "  root_workers=%d max_inflight=%d\n", plan.RootWorkers, plan.MaxInFlight)
	for _, ch := range annotated.Plan.Root.Children {
		writeNode(&b, "  ", ch, annotated, plan)
	}
	return b.String()
}

func writeNode(b *strings.Builder, indent string, n *logical.Node, annotated *estimate.AnnotatedPlan, plan *physical.Plan) {
	ann := annotated.Annotations[n.ID]
	strat := plan.Strategies[n.ID]
	fmt.Fprintf(b, "%s%s %s.%s -> %s\n", indent, n.Path, n.ParentType, n.FieldName, n.ReturnType)
	fmt.Fprintf(
		b,
		"%s  expected_rows=%.2f upper_bound_rows=%.2f parent_rows=%.2f logical_calls=%.2f physical_calls=%.2f total_calls=%.2f rows_out_total=%.2f\n",
		indent,
		ann.Card.RowsOutPerParent,
		ann.Card.UpperBoundRows,
		strat.Card.ParentRows,
		strat.Card.LogicalCalls,
		strat.Card.PhysicalCalls,
		strat.Card.TotalCalls,
		strat.Card.RowsOutTotal,
	)
	fmt.Fprintf(
		b,
		"%s  startup=%.2f self=%.2f children=%.2f total=%.2f width=%.0f conf=%.2f row_conf=%.2f lat_conf=%.2f source=%s upper_source=%s\n",
		indent,
		strat.Cost.StartupCost,
		strat.Cost.SelfCost,
		strat.Cost.ChildrenCost,
		strat.Cost.TotalCost,
		strat.Cost.WidthBytes,
		strat.Cost.Confidence,
		strat.Card.RowConfidence,
		strat.Cost.LatencyConfidence,
		strat.Card.Source,
		strat.Card.UpperBoundSource,
	)
	fmt.Fprintf(
		b,
		"%s  chosen_path=%s considered_paths=%d fields_mode=%s list_mode=%s workers=%d batch_size=%d max_concurrent_batches=%d cache=%t batch=%t peak_inflight=%.0f peak_memory=%.0f reason=%s\n",
		indent,
		strat.PathLabel,
		strat.ConsideredPaths,
		displayFieldsMode(strat.FieldsMode),
		displayListMode(strat.ListMode),
		strat.Workers,
		strat.BatchSize,
		strat.MaxConcurrentBatches,
		strat.UseCache,
		strat.UseBatching,
		strat.PeakInflight,
		strat.PeakMemory,
		strat.Reason,
	)
	if len(strat.CandidateTrace) > 1 {
		fmt.Fprintf(b, "%s  considered:\n", indent)
		for _, tr := range strat.CandidateTrace {
			fmt.Fprintf(
				b,
				"%s    - %s total=%.2f physical_calls=%.2f peak_inflight=%.0f peak_memory=%.0f workers=%d batch_size=%d fields_mode=%s list_mode=%s\n",
				indent,
				tr.PathLabel,
				tr.TotalCost,
				tr.PhysicalCalls,
				tr.PeakInflight,
				tr.PeakMemory,
				tr.Workers,
				tr.BatchSize,
				displayFieldsMode(tr.FieldsMode),
				displayListMode(tr.ListMode),
			)
		}
	}
	for _, ch := range n.Children {
		writeNode(b, indent+"    ", ch, annotated, plan)
	}
}

func displayFieldsMode(v physical.FieldsMode) string {
	if v == "" {
		return "-"
	}
	return string(v)
}

func displayListMode(v physical.ListMode) string {
	if v == "" {
		return "-"
	}
	return string(v)
}
