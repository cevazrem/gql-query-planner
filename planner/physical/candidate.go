package physical

type CandidateCardinality struct {
	ExpectedRows   float64
	UpperBoundRows float64
	Source         string
	UpperSource    string

	ParentRows    float64
	LogicalCalls  float64
	PhysicalCalls float64
	RowsOutTotal  float64
}

type CandidateCost struct {
	Startup  float64
	Self     float64
	Children float64
	Total    float64
	Width    float64
}

type CandidateResources struct {
	PeakInflight          float64
	PeakMemory            float64
	DescendantConcurrency float64
}

type CandidateRisk struct {
	RowConfidence     float64
	LatencyConfidence float64
}

type CandidateTrace struct {
	PathLabel     string
	TotalCost     float64
	PhysicalCalls float64
	PeakInflight  float64
	PeakMemory    float64
	BatchSize     int
	Workers       int
	ListMode      ListMode
	FieldsMode    FieldsMode
}

type CandidatePath struct {
	NodeID          string
	PathLabel       string
	ConsideredPaths int
	Trace           []CandidateTrace

	Strategy NodeStrategy

	Card      CandidateCardinality
	Cost      CandidateCost
	Resources CandidateResources
	Risk      CandidateRisk

	Children []*CandidatePath
}
