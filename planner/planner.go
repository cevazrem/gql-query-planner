package planner

import (
	"fmt"
	"time"

	"github.com/cevazrem/gql-query-planner/planner/catalog"
	"github.com/cevazrem/gql-query-planner/planner/estimate"
	"github.com/cevazrem/gql-query-planner/planner/explain"
	"github.com/cevazrem/gql-query-planner/planner/frontend"
	"github.com/cevazrem/gql-query-planner/planner/logical"
	"github.com/cevazrem/gql-query-planner/planner/optimize"
	"github.com/cevazrem/gql-query-planner/planner/physical"
	"github.com/cevazrem/gql-query-planner/planner/registry"
	"github.com/cevazrem/gql-query-planner/planner/stats"

	"github.com/vektah/gqlparser/v2/ast"
)

type Planner struct {
	schema  *ast.Schema
	catalog *catalog.Catalog
	stats   *stats.Store
	optCfg  optimize.Config
	builder *logical.Builder
}

type PlanTimings struct {
	BuildUs    int64
	AnnotateUs int64
	OptimizeUs int64
	TotalUs    int64

	LogicalNodes int
	StatsKeys    int
}

func New(schema *ast.Schema) (*Planner, error) {
	if schema == nil {
		return nil, fmt.Errorf("gqlplanner: schema is nil")
	}
	cat, err := catalog.New(schema)
	if err != nil {
		return nil, err
	}
	store := stats.NewStore()
	return &Planner{schema: schema, catalog: cat, stats: store, optCfg: optimize.DefaultConfig(), builder: logical.NewBuilder(cat)}, nil
}

func (e *Planner) Prepare(req frontend.Request) (*frontend.PreparedQuery, error) {
	return frontend.Prepare(e.schema, req)
}

func (e *Planner) Plan(prepared *frontend.PreparedQuery) (*estimate.AnnotatedPlan, *physical.Plan, error) {
	annotated, physicalPlan, _, err := e.PlanWithTimings(prepared)
	return annotated, physicalPlan, err
}

func (e *Planner) PlanWithTimings(prepared *frontend.PreparedQuery) (*estimate.AnnotatedPlan, *physical.Plan, PlanTimings, error) {
	totalStart := time.Now()

	buildStart := time.Now()
	logicalPlan, err := e.builder.Build(prepared)
	buildUs := time.Since(buildStart).Microseconds()
	if err != nil {
		return nil, nil, PlanTimings{BuildUs: buildUs, TotalUs: time.Since(totalStart).Microseconds()}, err
	}

	annotateStart := time.Now()
	annotated := estimate.Annotate(logicalPlan, e.stats)
	annotateUs := time.Since(annotateStart).Microseconds()

	optimizeStart := time.Now()
	physicalPlan := optimize.Choose(annotated, e.optCfg)
	optimizeUs := time.Since(optimizeStart).Microseconds()

	timings := PlanTimings{
		BuildUs:      buildUs,
		AnnotateUs:   annotateUs,
		OptimizeUs:   optimizeUs,
		TotalUs:      time.Since(totalStart).Microseconds(),
		LogicalNodes: countLogicalNodes(logicalPlan.Root),
	}
	if e.stats != nil {
		timings.StatsKeys = e.stats.Len()
	}

	return annotated, physicalPlan, timings, nil
}

func countLogicalNodes(n *logical.Node) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, ch := range n.Children {
		total += countLogicalNodes(ch)
	}
	return total
}

func (e *Planner) Explain(req frontend.Request) (string, error) {
	prepared, err := e.Prepare(req)
	if err != nil {
		return "", err
	}
	annotated, physicalPlan, err := e.Plan(prepared)
	if err != nil {
		return "", err
	}
	return explain.Text(annotated, physicalPlan), nil
}

func (e *Planner) Stats() *stats.Store {
	return e.stats
}

func (e *Planner) ValidateRuntimeRegistry(reg *registry.Registry) error {
	if e == nil || e.catalog == nil || reg == nil {
		return nil
	}
	for _, meta := range e.catalog.Fields() {
		if !meta.Caps.Batchable {
			continue
		}
		spec, ok := reg.Lookup(meta.ParentType, meta.FieldName)
		if !ok {
			return fmt.Errorf("gqlplanner: batchable field %s.%s has no registered field spec", meta.ParentType, meta.FieldName)
		}
		if spec.Direct == nil {
			return fmt.Errorf("gqlplanner: batchable field %s.%s is missing resolver implementation", meta.ParentType, meta.FieldName)
		}
	}
	return nil
}
