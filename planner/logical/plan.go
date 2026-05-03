package logical

import "github.com/cevazrem/gql-query-planner/planner/catalog"

type NodeKind string

const (
	NodeResolveField NodeKind = "RESOLVE_FIELD"
)

type Plan struct {
	QueryFingerprint string
	OperationName    string
	OperationType    string
	RootType         string
	Root             *Node
}

type Node struct {
	ID                string
	ParentType        string
	FieldName         string
	ResponseKey       string
	Path              string
	ReturnType        string
	ElemType          string
	ArgShape          string
	Kind              NodeKind
	IsList            bool
	Depth             int
	Args              map[string]any
	ParentResolverKey string
	ParentArgs        map[string]any
	Meta              catalog.FieldMeta
	Children          []*Node
}

func (n *Node) ResolverKey() string {
	return n.ParentType + "." + n.FieldName
}
