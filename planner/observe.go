package planner

import (
	"sort"

	"github.com/cevazrem/gql-query-planner/planner/logical"
)

func childResponseKeys(node *logical.Node) []string {
	if node == nil || len(node.Children) == 0 {
		return nil
	}
	keys := make([]string, 0, len(node.Children))
	for _, ch := range node.Children {
		if ch != nil {
			keys = append(keys, ch.ResponseKey)
		}
	}
	sort.Strings(keys)
	return keys
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
