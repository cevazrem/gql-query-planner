package graphqlschema

import "embed"

// FS contains raw GraphQL SDL files, including custom planner directives
//
//go:embed directives schema
var FS embed.FS
