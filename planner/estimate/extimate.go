package estimate

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/cevazrem/gql-query-planner/planner/logical"
	"github.com/cevazrem/gql-query-planner/planner/stats"
)

type Source string

const (
	SourceDefault              Source = "default"
	SourceStatsP50             Source = "stats:p50"
	SourceStatsBlend           Source = "stats:blend"
	SourceStatsP95             Source = "stats:p95"
	SourceStatsP99             Source = "stats:p99"
	SourceHint                 Source = "hint"
	SourceArgUpperBound        Source = "arg_upper_bound"
	SourceInheritedParentBound Source = "inherited_parent_bound"
)

type CardinalityEstimate struct {
	CallsPerParent    float64
	RowsOutPerParent  float64
	UpperBoundRows    float64
	ParentRows        float64
	LogicalCalls      float64
	PhysicalCalls     float64
	TotalCalls        float64
	RowsOutTotal      float64
	Confidence        float64
	RowConfidence     float64
	LatencyConfidence float64
	Source            Source
	UpperBoundSource  Source
}

type CostEstimate struct {
	StartupPerCall float64
	SelfPerCall    float64

	StartupCost  float64
	SelfCost     float64
	ChildrenCost float64
	TotalCost    float64

	WidthBytes        float64
	CPUCost           float64
	NetCost           float64
	MemoryCost        float64
	RiskPenalty       float64
	Confidence        float64
	RowConfidence     float64
	LatencyConfidence float64
}

type NodeAnnotation struct {
	Card CardinalityEstimate
	Cost CostEstimate
}

type AnnotatedPlan struct {
	Plan        *logical.Plan
	Annotations map[string]NodeAnnotation
}

type annotationContext struct {
	store     *stats.Store
	snapshots map[string]stats.FieldStatsSnapshot
	hits      map[string]bool
}

func newAnnotationContext(store *stats.Store) *annotationContext {
	return &annotationContext{
		store:     store,
		snapshots: map[string]stats.FieldStatsSnapshot{},
		hits:      map[string]bool{},
	}
}

func Annotate(p *logical.Plan, store *stats.Store) *AnnotatedPlan {
	out := &AnnotatedPlan{Plan: p, Annotations: map[string]NodeAnnotation{}}
	if p == nil || p.Root == nil {
		return out
	}

	annotateExecutionShape(p.Root, 1, out, newAnnotationContext(store))
	aggregateCosts(p.Root, out)
	return out
}

func annotateExecutionShape(n *logical.Node, parentRows float64, out *AnnotatedPlan, annCtx *annotationContext) {
	if n == nil {
		return
	}
	if parentRows <= 0 {
		parentRows = 1
	}

	if n.ID != "root" {
		card := estimateCardinalityLocal(n, parentRows, annCtx)
		card.ParentRows = parentRows
		card.LogicalCalls = parentRows * card.CallsPerParent
		card.PhysicalCalls = card.LogicalCalls
		card.TotalCalls = card.LogicalCalls
		card.RowsOutTotal = card.LogicalCalls * card.RowsOutPerParent

		out.Annotations[n.ID] = NodeAnnotation{
			Card: card,
			Cost: estimateLocalCost(n, card, parentRows, annCtx),
		}
		parentRows = card.RowsOutTotal
	}

	for _, ch := range n.Children {
		annotateExecutionShape(ch, parentRows, out, annCtx)
	}
}

func aggregateCosts(n *logical.Node, out *AnnotatedPlan) float64 {
	if n == nil || n.ID == "root" {
		total := 0.0
		for _, ch := range n.Children {
			total += aggregateCosts(ch, out)
		}
		return total
	}

	childrenTotal := 0.0
	for _, ch := range n.Children {
		childrenTotal += aggregateCosts(ch, out)
	}

	ann := out.Annotations[n.ID]
	ann.Cost.StartupCost = ann.Cost.StartupPerCall * ann.Card.PhysicalCalls
	ann.Cost.SelfCost = ann.Cost.SelfPerCall * ann.Card.LogicalCalls
	ann.Cost.ChildrenCost = childrenTotal
	ann.Cost.TotalCost = ann.Cost.StartupCost + ann.Cost.SelfCost + ann.Cost.ChildrenCost
	out.Annotations[n.ID] = ann
	return ann.Cost.TotalCost
}

func estimateCardinalityLocal(n *logical.Node, parentRows float64, annCtx *annotationContext) CardinalityEstimate {
	if !n.IsList {
		return CardinalityEstimate{
			CallsPerParent:    1,
			RowsOutPerParent:  1,
			UpperBoundRows:    1,
			Confidence:        1,
			RowConfidence:     1,
			LatencyConfidence: 1,
			Source:            SourceDefault,
			UpperBoundSource:  SourceDefault,
		}
	}

	upper, upperSrc := upperBoundForList(n)
	baseline, baselineSource, baselineConf := baselineExpectedRows(n, upper, upperSrc)
	expected := baseline
	source := baselineSource
	conf := baselineConf

	if annCtx != nil && annCtx.store != nil {
		if snap, ok := snapshotForNode(annCtx, n, parentRows); ok && snap.Calls >= 5 {
			if chosen, chosenSource, chosenConf, ok := chooseExpectedRowsFromStats(snap, upper); ok {
				weight := rowStatsAdoptionWeight(snap, n.Meta.Caps.Cacheable)
				expected = blendTowards(baseline, chosen, weight)
				source = chosenSource
				conf = math.Max(baselineConf, chosenConf*weight)
			}
			if upperSrc == SourceDefault && snap.RowConfidence >= 0.55 && snap.LenP99 > upper {
				upper = snap.LenP99
				upperSrc = SourceStatsP99
			}
		}
	}

	if upper <= 0 {
		upper = expected
		upperSrc = source
	}
	if expected <= 0 {
		expected = 1
	}

	return CardinalityEstimate{
		CallsPerParent:    1,
		RowsOutPerParent:  expected,
		UpperBoundRows:    upper,
		Confidence:        clamp01(conf),
		RowConfidence:     clamp01(conf),
		LatencyConfidence: clamp01(conf),
		Source:            source,
		UpperBoundSource:  upperSrc,
	}
}

func chooseExpectedRowsFromStats(snap stats.FieldStatsSnapshot, upper float64) (float64, Source, float64, bool) {
	if snap.Calls < 5 {
		return 0, SourceDefault, 0, false
	}

	if stableListFastPath(snap) {
		choose := maxPositive(snap.LenP50, snap.LenP95, snap.LenP99)
		if upper > 0 {
			choose = math.Min(choose, upper)
		}
		if choose > 0 {
			conf := math.Max(snap.RowConfidence, 0.70)
			return choose, SourceStatsP50, conf, true
		}
	}

	choose := 0.0
	source := SourceDefault
	conf := snap.RowConfidence
	switch {
	case snap.RowConfidence >= 0.75 && snap.LenP50 > 0:
		choose = snap.LenP50
		source = SourceStatsP50
	case snap.RowConfidence >= 0.50 && (snap.LenP50 > 0 || snap.LenP95 > 0):
		base := snap.LenP50
		if base <= 0 {
			base = snap.LenP95
		}
		higher := snap.LenP95
		if higher <= 0 {
			higher = base
		}
		choose = 0.65*base + 0.35*higher
		source = SourceStatsBlend
	case snap.RowConfidence >= 0.30 && snap.LenP95 > 0:
		choose = snap.LenP95
		source = SourceStatsP95
	default:
		return 0, SourceDefault, 0, false
	}

	if upper > 0 {
		choose = math.Min(choose, upper)
	}
	if choose <= 0 {
		return 0, SourceDefault, 0, false
	}
	return choose, source, conf, true
}

func baselineExpectedRows(n *logical.Node, upper float64, upperSrc Source) (float64, Source, float64) {
	if hinted := n.Meta.Hints.ExpectedFanout; hinted > 0 {
		expected := float64(hinted)
		if upper > 0 {
			expected = math.Min(expected, upper)
		}
		return expected, SourceHint, 0.45
	}
	if upper > 0 {
		return upper, upperSrc, 0.25
	}
	return 10, SourceDefault, 0.20
}

func rowStatsAdoptionWeight(snap stats.FieldStatsSnapshot, cacheable bool) float64 {
	sample := math.Min(1, float64(snap.Calls)/10.0)
	base := clamp01((snap.RowConfidence - 0.20) / 0.50)
	w := 0.15 + 0.70*sample*base
	if stableListFastPath(snap) {
		w += 0.10
	}
	cap := 0.85
	if cacheable {
		cap = 0.70
	}
	if w > cap {
		w = cap
	}
	return clamp01(w)
}

func latencyStatsAdoptionWeight(snap stats.FieldStatsSnapshot, cacheable bool) float64 {
	sample := math.Min(1, float64(snap.Calls)/12.0)
	base := clamp01((snap.LatencyConfidence - 0.20) / 0.55)
	w := 0.10 + 0.50*sample*base
	cap := 0.55
	if cacheable {
		cap = 0.35
	}
	if w > cap {
		w = cap
	}
	return clamp01(w)
}

func widthStatsAdoptionWeight(snap stats.FieldStatsSnapshot, cacheable bool) float64 {
	sample := math.Min(1, float64(snap.Calls)/12.0)
	base := clamp01((snap.RowConfidence - 0.20) / 0.55)
	w := 0.10 + 0.45*sample*base
	cap := 0.60
	if cacheable {
		cap = 0.40
	}
	if w > cap {
		w = cap
	}
	return clamp01(w)
}

func blendTowards(base, observed, weight float64) float64 {
	if observed <= 0 {
		return base
	}
	if base <= 0 {
		base = observed
	}
	weight = clamp01(weight)
	return base*(1-weight) + observed*weight
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func stableListFastPath(snap stats.FieldStatsSnapshot) bool {
	if snap.Calls < 5 {
		return false
	}
	if snap.RowStabilityScore < 0.95 {
		return false
	}
	if snap.LenP50 <= 0 || snap.LenP95 <= 0 || snap.LenP99 <= 0 {
		return false
	}
	return almostEqual(snap.LenP50, snap.LenP95) && almostEqual(snap.LenP95, snap.LenP99)
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.0001
}

func upperBoundForList(n *logical.Node) (float64, Source) {
	if lim, ok := numericArgLimitDeep(n.Args); ok && lim > 0 {
		return float64(lim), SourceArgUpperBound
	}
	if lim, ok := inheritedParentUpperBound(n); ok && lim > 0 {
		return float64(lim), SourceInheritedParentBound
	}
	if hinted := n.Meta.Hints.ExpectedFanout; hinted > 0 {
		return float64(hinted), SourceHint
	}
	return 10, SourceDefault
}

func inheritedParentUpperBound(n *logical.Node) (int, bool) {
	if n == nil || !n.IsList || len(n.Args) > 0 {
		return 0, false
	}
	if n.ParentArgs == nil {
		return 0, false
	}
	if strings.HasSuffix(n.ParentType, "Payload") || strings.HasSuffix(n.ParentResolverKey, ".edges") || strings.HasSuffix(n.ParentResolverKey, ".nodes") {
		return numericArgLimitDeep(n.ParentArgs)
	}
	return 0, false
}

func estimateLocalCost(n *logical.Node, card CardinalityEstimate, parentRows float64, annCtx *annotationContext) CostEstimate {
	width := estimateWidth(n)
	startup := 1.0
	cpu := n.Meta.Hints.CostWeight
	if cpu <= 0 {
		cpu = 1
	}
	risk := uncertaintyPenalty(card.Confidence)
	costConf := card.Confidence

	if annCtx != nil && annCtx.store != nil {
		if snap, ok := snapshotForNode(annCtx, n, parentRows); ok {
			latW := latencyStatsAdoptionWeight(snap, n.Meta.Caps.Cacheable)
			widthW := widthStatsAdoptionWeight(snap, n.Meta.Caps.Cacheable)
			if snap.LatencyMsP50 > 0 {
				startup = blendTowards(startup, math.Max(1, snap.LatencyMsP50/5), latW)
			}
			risk += snap.ErrorRate * 10
			if snap.WidthBytesP50 > 0 {
				width = blendTowards(width, snap.WidthBytesP50, widthW)
			}
			if snap.LatencyMsP95 > 0 {
				perItemLatency := snap.LatencyMsP95 / math.Max(1, maxPositive(snap.LenP50, card.RowsOutPerParent))
				cpu += 0.10 * perItemLatency * (0.5 + latW)
			}
			risk += (1 - snap.LatencyStabilityScore) * 3
			risk += (1 - snap.FreshnessScore) * 2
			costConf = minFloat(card.Confidence, blendTowards(card.Confidence, snap.LatencyConfidence, latW))
		}
	}

	net := 0.5 * card.RowsOutPerParent
	mem := (width * card.RowsOutPerParent) / 4096.0
	self := cpu + net + mem + risk

	return CostEstimate{
		StartupPerCall:    startup,
		SelfPerCall:       self,
		WidthBytes:        width,
		CPUCost:           cpu,
		NetCost:           net,
		MemoryCost:        mem,
		RiskPenalty:       risk,
		Confidence:        costConf,
		RowConfidence:     card.RowConfidence,
		LatencyConfidence: costConf,
	}
}

func uncertaintyPenalty(conf float64) float64 {
	if conf >= 0.85 {
		return 0.10
	}
	if conf >= 0.55 {
		return 0.40
	}
	if conf >= 0.30 {
		return 1.25
	}
	return 3.00
}

func estimateWidth(n *logical.Node) float64 {
	if n.Meta.IsScalar {
		return 32
	}
	if n.IsList {
		return 128
	}
	return 96
}

func numericArgLimitDeep(args map[string]any) (int, bool) {
	for _, key := range []string{"first", "last", "limit", "perPage", "pageSize", "take"} {
		if v, ok := args[key]; ok {
			if n, ok := toInt(v); ok && n > 0 {
				return n, true
			}
		}
	}

	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch child := args[k].(type) {
		case map[string]any:
			if n, ok := numericArgLimitDeep(child); ok && n > 0 {
				return n, true
			}
		case []any:
			for _, item := range child {
				if m, ok := item.(map[string]any); ok {
					if n, ok := numericArgLimitDeep(m); ok && n > 0 {
						return n, true
					}
				}
			}
		}
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int8:
		return int(x), true
	case int16:
		return int(x), true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float32:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil
	default:
		return 0, false
	}
}

func maxPositive(vals ...float64) float64 {
	best := 0.0
	for _, v := range vals {
		if v > best {
			best = v
		}
	}
	if best <= 0 {
		return 1
	}
	return best
}

func snapshotForNode(annCtx *annotationContext, n *logical.Node, parentRows float64) (stats.FieldStatsSnapshot, bool) {
	if annCtx == nil || annCtx.store == nil || n == nil {
		return stats.FieldStatsSnapshot{}, false
	}
	key := statsKeyForNode(n, parentRows)
	cacheKey := key.String()
	if hit, ok := annCtx.hits[cacheKey]; ok {
		return annCtx.snapshots[cacheKey], hit
	}

	snap, ok := annCtx.store.SnapshotWithParentFallback(key)
	annCtx.hits[cacheKey] = ok
	if ok {
		annCtx.snapshots[cacheKey] = snap
	}
	return snap, ok
}

func statsKeyForNode(n *logical.Node, parentRows float64) stats.FieldKey {
	childShape := ""
	if len(n.Children) > 0 {
		keys := make([]string, 0, len(n.Children))
		for _, ch := range n.Children {
			keys = append(keys, ch.ResponseKey)
		}
		sort.Strings(keys)
		childShape = strings.Join(keys, ",")
		if childShape == "" {
			childShape = "-"
		}
	}
	return stats.FieldKey{
		ResolverKey:             n.ResolverKey(),
		DepthBucket:             depthBucket(n.Depth),
		ArgShape:                n.ArgShape,
		SelectedChildrenShape:   childShape,
		ParentCardinalityBucket: cardinalityBucket(parentRows),
	}
}

func depthBucket(depth int) string {
	switch {
	case depth <= 1:
		return "1"
	case depth == 2:
		return "2"
	case depth <= 4:
		return "3-4"
	case depth <= 8:
		return "5-8"
	default:
		return "9+"
	}
}

func cardinalityBucket(v float64) string {
	switch {
	case v <= 1:
		return "1"
	case v <= 5:
		return "2-5"
	case v <= 10:
		return "6-10"
	case v <= 25:
		return "11-25"
	case v <= 100:
		return "26-100"
	case v <= 500:
		return "101-500"
	default:
		return "500+"
	}
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
