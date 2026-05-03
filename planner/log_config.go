package planner

type LogConfig struct {
	// LogPlans controls verbose textual plan dumps:
	//   - QUERY PLAN
	//   - QUERY PLAN ANALYZE
	//
	// Timing logs like prepare_us/plan_us are intentionally left enabled.
	LogPlans bool

	// LogPlanBreakdown adds one compact diagnostic line with separate
	// build/annotate/optimize timings. It is useful for profiling large queries.
	LogPlanBreakdown bool
}
