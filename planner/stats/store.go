package stats

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type FieldKey struct {
	ResolverKey             string
	DepthBucket             string
	ArgShape                string
	SelectedChildrenShape   string
	ParentCardinalityBucket string
}

func (k FieldKey) String() string {
	return fmt.Sprintf("%s|d=%s|args=%s|child=%s|pc=%s", k.ResolverKey, k.DepthBucket, k.ArgShape, k.SelectedChildrenShape, emptyDash(k.ParentCardinalityBucket))
}

type FieldStatsSnapshot struct {
	Calls                 int64
	Errors                int64
	ErrorRate             float64
	LenP50                float64
	LenP95                float64
	LenP99                float64
	LatencyMsP50          float64
	LatencyMsP95          float64
	LatencyMsP99          float64
	WidthBytesP50         float64
	WidthBytesP95         float64
	WidthBytesP99         float64
	Confidence            float64
	RowConfidence         float64
	LatencyConfidence     float64
	SampleConfidence      float64
	FreshnessScore        float64
	StabilityScore        float64
	RowStabilityScore     float64
	LatencyStabilityScore float64
	DistinctShapes        int
	LastSeenAt            time.Time
}

type ExportedFieldStats struct {
	Key      FieldKey
	KeyText  string
	Snapshot FieldStatsSnapshot
}

type fieldSeries struct {
	key        FieldKey
	calls      int64
	errors     int64
	lens       []float64
	latencyMs  []float64
	widthBytes []float64
	shapeSet   map[string]struct{}
	lastSeenAt time.Time
}

type Store struct {
	mu     sync.RWMutex
	fields map[string]*fieldSeries

	// parentlessIndex groups field series by the same lookup key without
	// ParentCardinalityBucket. Planning often asks for statistics with a
	// freshly estimated parent-cardinality bucket; if an exact bucket was not
	// observed yet, fallback must not scan the whole statistics store.
	parentlessIndex map[string][]*fieldSeries
}

func NewStore() *Store {
	return &Store{
		fields:          map[string]*fieldSeries{},
		parentlessIndex: map[string][]*fieldSeries{},
	}
}

type Observation struct {
	Key              FieldKey
	ObservedLen      int
	LatencyMs        float64
	WidthBytes       float64
	HadError         bool
	SelectedChildren []string
	ObservedAt       time.Time
}

func (s *Store) Record(obs Observation) {
	if s == nil {
		return
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = time.Now()
	}

	k := obs.Key.String()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fields == nil {
		s.fields = map[string]*fieldSeries{}
	}
	if s.parentlessIndex == nil {
		s.parentlessIndex = map[string][]*fieldSeries{}
	}

	fs := s.fields[k]
	if fs == nil {
		fs = &fieldSeries{key: obs.Key, shapeSet: map[string]struct{}{}}
		s.fields[k] = fs
		s.parentlessIndex[obs.Key.ParentlessString()] = append(s.parentlessIndex[obs.Key.ParentlessString()], fs)
	}
	fs.calls++
	fs.lastSeenAt = obs.ObservedAt
	if obs.HadError {
		fs.errors++
	}
	if obs.ObservedLen >= 0 {
		fs.lens = append(fs.lens, float64(obs.ObservedLen))
	}
	if obs.LatencyMs >= 0 {
		fs.latencyMs = append(fs.latencyMs, obs.LatencyMs)
	}
	if obs.WidthBytes >= 0 {
		fs.widthBytes = append(fs.widthBytes, obs.WidthBytes)
	}
	if len(obs.SelectedChildren) > 0 {
		fs.shapeSet[fmt.Sprint(obs.SelectedChildren)] = struct{}{}
	}
}

func (s *Store) Snapshot(key FieldKey) (FieldStatsSnapshot, bool) {
	if s == nil {
		return FieldStatsSnapshot{}, false
	}
	s.mu.RLock()
	fs := s.fields[key.String()]
	if fs == nil {
		s.mu.RUnlock()
		return FieldStatsSnapshot{}, false
	}
	rec := copySeries(fs)
	s.mu.RUnlock()
	return snapshotFromSeries(rec), true
}

func (s *Store) SnapshotWithParentFallback(key FieldKey) (FieldStatsSnapshot, bool) {
	if s == nil {
		return FieldStatsSnapshot{}, false
	}

	s.mu.RLock()
	if fs := s.fields[key.String()]; fs != nil {
		rec := copySeries(fs)
		s.mu.RUnlock()
		return snapshotFromSeries(rec), true
	}

	candidates := s.parentlessIndex[key.ParentlessString()]
	if len(candidates) == 0 {
		s.mu.RUnlock()
		return FieldStatsSnapshot{}, false
	}

	var best fieldSeries
	found := false
	var bestSnap FieldStatsSnapshot
	for _, fs := range candidates {
		if fs == nil {
			continue
		}
		rec := copySeries(fs)
		snap := snapshotFromSeries(rec)
		if !found || betterFallbackSnapshot(snap, bestSnap, rec, best) {
			best = rec
			bestSnap = snap
			found = true
		}
	}
	s.mu.RUnlock()
	if !found {
		return FieldStatsSnapshot{}, false
	}
	return bestSnap, true
}

func (k FieldKey) ParentlessString() string {
	return fmt.Sprintf("%s|d=%s|args=%s|child=%s", k.ResolverKey, k.DepthBucket, k.ArgShape, k.SelectedChildrenShape)
}

func sameKeyExceptParentBucket(a, b FieldKey) bool {
	return a.ParentlessString() == b.ParentlessString()
}

func betterFallbackSnapshot(candidate, current FieldStatsSnapshot, candidateSeries, currentSeries fieldSeries) bool {
	if candidate.Calls != current.Calls {
		return candidate.Calls > current.Calls
	}
	if math.Abs(candidate.RowConfidence-current.RowConfidence) > 1e-9 {
		return candidate.RowConfidence > current.RowConfidence
	}
	if !candidateSeries.lastSeenAt.Equal(currentSeries.lastSeenAt) {
		return candidateSeries.lastSeenAt.After(currentSeries.lastSeenAt)
	}
	return candidateSeries.key.String() < currentSeries.key.String()
}

func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.fields)
}

func (s *Store) Export() []ExportedFieldStats {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	items := make([]ExportedFieldStats, 0, len(s.fields))
	for keyText, fs := range s.fields {
		rec := copySeries(fs)
		items = append(items, ExportedFieldStats{
			Key:      rec.key,
			KeyText:  keyText,
			Snapshot: snapshotFromSeries(rec),
		})
	}
	s.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].Snapshot.RowConfidence == items[j].Snapshot.RowConfidence {
			return items[i].KeyText < items[j].KeyText
		}
		return items[i].Snapshot.RowConfidence > items[j].Snapshot.RowConfidence
	})
	return items
}

func (s *Store) ExportFiltered(resolverContains string) []ExportedFieldStats {
	items := s.Export()
	if resolverContains == "" {
		return items
	}
	needle := strings.ToLower(resolverContains)
	out := make([]ExportedFieldStats, 0, len(items))
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Key.ResolverKey), needle) || strings.Contains(strings.ToLower(it.KeyText), needle) {
			out = append(out, it)
		}
	}
	return out
}

func copySeries(fs *fieldSeries) fieldSeries {
	return fieldSeries{
		key:        fs.key,
		calls:      fs.calls,
		errors:     fs.errors,
		lens:       append([]float64(nil), fs.lens...),
		latencyMs:  append([]float64(nil), fs.latencyMs...),
		widthBytes: append([]float64(nil), fs.widthBytes...),
		shapeSet:   cloneSet(fs.shapeSet),
		lastSeenAt: fs.lastSeenAt,
	}
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func snapshotFromSeries(fs fieldSeries) FieldStatsSnapshot {
	errorRate := safeRate(fs.errors, fs.calls)
	sample := sampleConfidence(fs.calls)
	fresh := freshnessScore(fs.lastSeenAt)
	rowStability := rowStabilityScore(fs.lens, errorRate)
	latStability := latencyStabilityScore(fs.latencyMs, errorRate)
	rowConf := clamp01(sample * fresh * rowStability * (1 - 0.25*minFloat(errorRate, 1)))
	latConf := clamp01(sample * fresh * latStability * (1 - 0.50*minFloat(errorRate, 1)))
	overall := minFloat(rowConf, latConf)

	return FieldStatsSnapshot{
		Calls:                 fs.calls,
		Errors:                fs.errors,
		ErrorRate:             errorRate,
		LenP50:                quantile(fs.lens, 0.50),
		LenP95:                quantile(fs.lens, 0.95),
		LenP99:                quantile(fs.lens, 0.99),
		LatencyMsP50:          quantile(fs.latencyMs, 0.50),
		LatencyMsP95:          quantile(fs.latencyMs, 0.95),
		LatencyMsP99:          quantile(fs.latencyMs, 0.99),
		WidthBytesP50:         quantile(fs.widthBytes, 0.50),
		WidthBytesP95:         quantile(fs.widthBytes, 0.95),
		WidthBytesP99:         quantile(fs.widthBytes, 0.99),
		Confidence:            overall,
		RowConfidence:         rowConf,
		LatencyConfidence:     latConf,
		SampleConfidence:      sample,
		FreshnessScore:        fresh,
		StabilityScore:        overall,
		RowStabilityScore:     rowStability,
		LatencyStabilityScore: latStability,
		DistinctShapes:        len(fs.shapeSet),
		LastSeenAt:            fs.lastSeenAt,
	}
}

func safeRate(num, den int64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func sampleConfidence(calls int64) float64 {
	if calls <= 0 {
		return 0
	}
	base := math.Log1p(float64(calls)) / math.Log1p(100)
	if base > 1 {
		base = 1
	}
	if base < 0 {
		base = 0
	}
	return base
}

func freshnessScore(lastSeen time.Time) float64 {
	if lastSeen.IsZero() {
		return 0.4
	}
	ageHours := time.Since(lastSeen).Hours()
	if ageHours <= 0 {
		return 1
	}
	const halfLifeHours = 24.0
	return math.Exp(-math.Ln2 * ageHours / halfLifeHours)
}

func rowStabilityScore(lens []float64, errorRate float64) float64 {
	lenP50 := quantile(lens, 0.50)
	lenP95 := quantile(lens, 0.95)
	lenRatio := ratioOrOne(lenP95, lenP50)
	volPenalty := maxFloat(0.45, 1.0/(1.0+0.22*(lenRatio-1)))
	errorPenalty := maxFloat(0.5, 1.0-0.4*minFloat(errorRate, 1))
	return clamp01(volPenalty * errorPenalty)
}

func latencyStabilityScore(latencies []float64, errorRate float64) float64 {
	latP50 := quantile(latencies, 0.50)
	latP95 := quantile(latencies, 0.95)
	latRatio := ratioOrOne(latP95, latP50)
	volPenalty := maxFloat(0.35, 1.0/(1.0+0.25*(latRatio-1)))
	errorPenalty := maxFloat(0.4, 1.0-0.8*minFloat(errorRate, 1))
	return clamp01(volPenalty * errorPenalty)
}

func ratioOrOne(hi, lo float64) float64 {
	if lo <= 0 || hi <= 0 {
		return 1
	}
	if hi < lo {
		return 1
	}
	return hi / lo
}

func quantile(in []float64, q float64) float64 {
	if len(in) == 0 {
		return 0
	}
	cp := append([]float64(nil), in...)
	sort.Float64s(cp)
	if q <= 0 {
		return cp[0]
	}
	if q >= 1 {
		return cp[len(cp)-1]
	}
	idx := int(float64(len(cp)-1) * q)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
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

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
