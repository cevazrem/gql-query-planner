package planner

import (
	"sort"
	"strings"
	"time"

	"github.com/cevazrem/gql-query-planner/planner/stats"
)

type StatsExportRow struct {
	ResolverKey           string    `json:"resolver_key"`
	DepthBucket           string    `json:"depth_bucket"`
	ArgShape              string    `json:"arg_shape"`
	SelectedChildrenShape string    `json:"selected_children_shape"`
	ParentCardBucket      string    `json:"parent_cardinality_bucket"`
	Key                   string    `json:"key"`
	Calls                 int64     `json:"calls"`
	Errors                int64     `json:"errors"`
	ErrorRate             float64   `json:"error_rate"`
	LenP50                float64   `json:"len_p50"`
	LenP95                float64   `json:"len_p95"`
	LenP99                float64   `json:"len_p99"`
	LatencyMsP50          float64   `json:"latency_ms_p50"`
	LatencyMsP95          float64   `json:"latency_ms_p95"`
	LatencyMsP99          float64   `json:"latency_ms_p99"`
	WidthBytesP50         float64   `json:"width_bytes_p50"`
	WidthBytesP95         float64   `json:"width_bytes_p95"`
	WidthBytesP99         float64   `json:"width_bytes_p99"`
	Confidence            float64   `json:"confidence"`
	RowConfidence         float64   `json:"row_confidence"`
	LatencyConfidence     float64   `json:"latency_confidence"`
	SampleConfidence      float64   `json:"sample_confidence"`
	FreshnessScore        float64   `json:"freshness_score"`
	StabilityScore        float64   `json:"stability_score"`
	RowStabilityScore     float64   `json:"row_stability_score"`
	LatencyStabilityScore float64   `json:"latency_stability_score"`
	DistinctShapes        int       `json:"distinct_shapes"`
	LastSeenAt            time.Time `json:"last_seen_at"`
}

type StatsExportOptions struct {
	ResolverContains string
	Limit            int
	Sort             string
}

func (e *Planner) ExportStats(opts StatsExportOptions) []StatsExportRow {
	if e == nil || e.stats == nil {
		return nil
	}
	items := e.stats.ExportFiltered(opts.ResolverContains)
	rows := make([]StatsExportRow, 0, len(items))
	for _, it := range items {
		rows = append(rows, exportRow(it))
	}
	sortRows(rows, opts.Sort)
	if opts.Limit > 0 && len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
	}
	return rows
}

func exportRow(it stats.ExportedFieldStats) StatsExportRow {
	s := it.Snapshot
	return StatsExportRow{
		ResolverKey:           it.Key.ResolverKey,
		DepthBucket:           it.Key.DepthBucket,
		ArgShape:              it.Key.ArgShape,
		SelectedChildrenShape: it.Key.SelectedChildrenShape,
		ParentCardBucket:      it.Key.ParentCardinalityBucket,
		Key:                   it.KeyText,
		Calls:                 s.Calls,
		Errors:                s.Errors,
		ErrorRate:             s.ErrorRate,
		LenP50:                s.LenP50,
		LenP95:                s.LenP95,
		LenP99:                s.LenP99,
		LatencyMsP50:          s.LatencyMsP50,
		LatencyMsP95:          s.LatencyMsP95,
		LatencyMsP99:          s.LatencyMsP99,
		WidthBytesP50:         s.WidthBytesP50,
		WidthBytesP95:         s.WidthBytesP95,
		WidthBytesP99:         s.WidthBytesP99,
		Confidence:            s.Confidence,
		RowConfidence:         s.RowConfidence,
		LatencyConfidence:     s.LatencyConfidence,
		SampleConfidence:      s.SampleConfidence,
		FreshnessScore:        s.FreshnessScore,
		StabilityScore:        s.StabilityScore,
		RowStabilityScore:     s.RowStabilityScore,
		LatencyStabilityScore: s.LatencyStabilityScore,
		DistinctShapes:        s.DistinctShapes,
		LastSeenAt:            s.LastSeenAt,
	}
}

func sortRows(rows []StatsExportRow, sortBy string) {
	switch strings.ToLower(sortBy) {
	case "calls":
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Calls == rows[j].Calls {
				return rows[i].Key < rows[j].Key
			}
			return rows[i].Calls > rows[j].Calls
		})
	case "latency":
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].LatencyMsP95 == rows[j].LatencyMsP95 {
				return rows[i].Key < rows[j].Key
			}
			return rows[i].LatencyMsP95 > rows[j].LatencyMsP95
		})
	case "row_confidence":
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].RowConfidence == rows[j].RowConfidence {
				return rows[i].Key < rows[j].Key
			}
			return rows[i].RowConfidence > rows[j].RowConfidence
		})
	case "last_seen":
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].LastSeenAt.Equal(rows[j].LastSeenAt) {
				return rows[i].Key < rows[j].Key
			}
			return rows[i].LastSeenAt.After(rows[j].LastSeenAt)
		})
	default:
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Confidence == rows[j].Confidence {
				return rows[i].Key < rows[j].Key
			}
			return rows[i].Confidence > rows[j].Confidence
		})
	}
}
