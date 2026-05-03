package frontend

import (
	"github.com/vektah/gqlparser/v2/ast"
)

type Request struct {
	Query         string
	OperationName string
	Variables     map[string]any
}

type PreparedQuery struct {
	RawQuery        string
	OperationName   string
	Variables       map[string]any
	CanonicalDoc    *ast.QueryDocument
	CanonicalOp     *ast.OperationDefinition
	Fingerprint     string
	RootType        string
	NormalizedQuery string
}
