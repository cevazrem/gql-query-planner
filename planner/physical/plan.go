package physical

import "github.com/cevazrem/gql-query-planner/planner/estimate"

type FieldsMode string

type ListMode string

const (
	FieldsSequential FieldsMode = "SEQ_FIELDS"
	FieldsParallel   FieldsMode = "PAR_FIELDS"

	ListSerial   ListMode = "LIST_SERIAL"
	ListParallel ListMode = "LIST_PARALLEL"
	ListBatched  ListMode = "LIST_BATCHED"
)

type NodeStrategy struct {
	NodeID               string
	PathLabel            string
	ConsideredPaths      int
	CandidateTrace       []CandidateTrace
	FieldsMode           FieldsMode
	ListMode             ListMode
	Workers              int
	UseBatching          bool
	BatchSize            int
	MaxConcurrentBatches int
	UseCache             bool
	Reason               string
	PeakInflight         float64
	PeakMemory           float64
	Cost                 estimate.CostEstimate
	Card                 estimate.CardinalityEstimate
}

type Plan struct {
	RootWorkers int
	MaxInFlight int
	Strategies  map[string]NodeStrategy
}
