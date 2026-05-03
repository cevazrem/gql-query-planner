package planner

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/cevazrem/gql-query-planner/planner/execute"
	"github.com/cevazrem/gql-query-planner/planner/explain"
	"github.com/cevazrem/gql-query-planner/planner/frontend"
	"github.com/cevazrem/gql-query-planner/planner/registry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"google.golang.org/protobuf/encoding/protojson"
)

var query2Tracer = otel.Tracer("gqlplanner/query")

type httpGraphQLRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
}

type httpGraphQLError struct {
	Message string `json:"message"`
}

type httpGraphQLResponse struct {
	Data   any                `json:"data,omitempty"`
	Errors []httpGraphQLError `json:"errors,omitempty"`
}

func NewHTTPHandler(gqlPlanner *Planner, resolverRegistry *registry.Registry, logCfg LogConfig) http.Handler {
	if err := gqlPlanner.ValidateRuntimeRegistry(resolverRegistry); err != nil {
		panic(err)
	}
	exec := execute.New(resolverRegistry)
	marshal := protojson.MarshalOptions{UseProtoNames: false, EmitUnpopulated: false}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := query2Tracer.Start(r.Context(), "gqlplanner.query2")
		defer span.End()

		totalStart := time.Now()

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(httpGraphQLResponse{Errors: []httpGraphQLError{{Message: "only POST is supported"}}})
			return
		}

		var req httpGraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, "decode request")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(httpGraphQLResponse{Errors: []httpGraphQLError{{Message: err.Error()}}})
			return
		}

		span.SetAttributes(
			attribute.String("graphql.operation_name", req.OperationName),
			attribute.Int("graphql.query_length", len(req.Query)),
		)

		prepareStart := time.Now()
		_, prepareSpan := query2Tracer.Start(ctx, "gqlplanner.prepare")
		prepared, err := gqlPlanner.Prepare(frontend.Request{
			Query:         req.Query,
			OperationName: req.OperationName,
			Variables:     req.Variables,
		})
		if err != nil {
			prepareSpan.RecordError(err)
			prepareSpan.SetStatus(otelcodes.Error, "prepare failed")
			prepareSpan.End()

			span.RecordError(err)
			span.SetStatus(otelcodes.Error, "prepare failed")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(httpGraphQLResponse{Errors: []httpGraphQLError{{Message: err.Error()}}})
			return
		}
		prepareUs := time.Since(prepareStart).Microseconds()
		prepareSpan.SetAttributes(attribute.Int64("prepare.us", prepareUs))
		prepareSpan.End()

		planStart := time.Now()
		_, planSpan := query2Tracer.Start(ctx, "gqlplanner.plan")
		annotated, phys, planTimings, err := gqlPlanner.PlanWithTimings(prepared)
		if err != nil {
			planSpan.RecordError(err)
			planSpan.SetStatus(otelcodes.Error, "plan failed")
			planSpan.End()

			span.RecordError(err)
			span.SetStatus(otelcodes.Error, "plan failed")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(httpGraphQLResponse{Errors: []httpGraphQLError{{Message: err.Error()}}})
			return
		}
		var explainText string
		if logCfg.LogPlans {
			explainText = explain.Text(annotated, phys)
		}
		planUs := time.Since(planStart).Microseconds()
		planSpan.SetAttributes(
			attribute.Int64("plan.us", planUs),
			attribute.Int64("plan.build_us", planTimings.BuildUs),
			attribute.Int64("plan.annotate_us", planTimings.AnnotateUs),
			attribute.Int64("plan.optimize_us", planTimings.OptimizeUs),
			attribute.Int("plan.logical_nodes", planTimings.LogicalNodes),
			attribute.Int("plan.stats_keys", planTimings.StatsKeys),
			attribute.Int("plan.explain_length", len(explainText)),
		)
		planSpan.End()

		if logCfg.LogPlans {
			if explainText != "" {
				log.Printf("[gqlplanner-query] prepare_us=%d plan_us=%d\n%s", prepareUs, planUs, explainText)
			} else {
				log.Printf("[gqlplanner-query] prepare_us=%d plan_us=%d", prepareUs, planUs)
			}
		}

		if logCfg.LogPlanBreakdown {
			log.Printf(
				"[gqlplanner-plan-breakdown] build_us=%d annotate_us=%d optimize_us=%d total_us=%d logical_nodes=%d stats_keys=%d",
				planTimings.BuildUs,
				planTimings.AnnotateUs,
				planTimings.OptimizeUs,
				planTimings.TotalUs,
				planTimings.LogicalNodes,
				planTimings.StatsKeys,
			)
		}

		execStart := time.Now()
		execCtx, execSpan := query2Tracer.Start(ctx, "gqlplanner.execute")
		data, rt, err := exec.ExecuteRootWithRuntime(execCtx, annotated.Plan.Root, phys)
		if err != nil {
			execSpan.RecordError(err)
			execSpan.SetStatus(otelcodes.Error, "execute failed")
			execSpan.End()

			span.RecordError(err)
			span.SetStatus(otelcodes.Error, "execute failed")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(httpGraphQLResponse{Errors: []httpGraphQLError{{Message: err.Error()}}})
			return
		}
		execDuration := time.Since(execStart)
		execMs := execDuration.Milliseconds()
		execSpan.SetAttributes(attribute.Int64("execute.ms", execMs))
		execSpan.End()

		if logCfg.LogPlans {
			analyzeText := explain.AnalyzeText(annotated, phys, rt)
			if analyzeText != "" {
				log.Printf("[gqlplanner-query-analyze]\n%s", analyzeText)
			}
		}

		summary := gqlPlanner.recordRuntimeStats(annotated, phys, rt, execDuration)
		if logCfg.LogPlans {
			log.Printf("[gqlplanner-query-summary] %s", formatExecutionSummaryLines(summary))
		}

		encoded, err := normalizeForJSON(data, marshal)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, "normalize failed")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(httpGraphQLResponse{Errors: []httpGraphQLError{{Message: err.Error()}}})
			return
		}

		totalMs := time.Since(totalStart).Milliseconds()
		span.SetAttributes(
			attribute.Int64("query2.total_ms", totalMs),
			attribute.Int64("query2.prepare_us", prepareUs),
			attribute.Int64("query2.plan_us", planUs),
			attribute.Int64("query2.execute_ms", execMs),
		)
		if logCfg.LogPlans {
			log.Printf("[gqlplanner-query] total_ms=%d execute_ms=%d", totalMs, execMs)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-GQLPlanner-Prepare-Us", strconv.FormatInt(prepareUs, 10))
		w.Header().Set("X-GQLPlanner-Plan-Us", strconv.FormatInt(planUs, 10))
		w.Header().Set("X-GQLPlanner-Execute-Ms", strconv.FormatInt(execMs, 10))
		w.Header().Set("X-GQLPlanner-Total-Ms", strconv.FormatInt(totalMs, 10))

		_ = json.NewEncoder(w).Encode(httpGraphQLResponse{Data: encoded})
	})
}
