package planner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func NewFromSchemaDir(root string) (*Planner, error) {
	if root == "" {
		root = "."
	}
	sources, err := loadSchemaSourcesOS(root)
	if err != nil {
		return nil, err
	}
	schema, err := gqlparser.LoadSchema(sources...)
	if err != nil {
		return nil, fmt.Errorf("gqlplanner: load schema from files: %w", err)
	}
	return New(schema)
}

func NewFromSchemaString(input string) (*Planner, error) {
	if strings.TrimSpace(input) == "" {
		return nil, fmt.Errorf("gqlplanner: schema string is empty")
	}

	schema, err := gqlparser.LoadSchema(&ast.Source{
		Name:  "schema.graphql",
		Input: input,
	})
	if err != nil {
		return nil, fmt.Errorf("gqlplanner: load schema from string: %w", err)
	}

	return New(schema)
}

func NewFromFS(fsys fs.FS, root string) (*Planner, error) {
	if fsys == nil {
		return nil, fmt.Errorf("gqlplanner: fs is nil")
	}
	if root == "" {
		root = "."
	}
	sources, err := loadSchemaSourcesFS(fsys, root)
	if err != nil {
		return nil, err
	}
	schema, err := gqlparser.LoadSchema(sources...)
	if err != nil {
		return nil, fmt.Errorf("gqlplanner: load schema from embedded fs: %w", err)
	}
	return New(schema)
}

func loadSchemaSourcesOS(root string) ([]*ast.Source, error) {
	files := make([]string, 0, 16)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".graphql") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("gqlplanner: no .graphql files found under %s", root)
	}

	sources := make([]*ast.Source, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		sources = append(sources, &ast.Source{
			Name:  f,
			Input: string(b),
		})
	}
	return sources, nil
}

func loadSchemaSourcesFS(fsys fs.FS, root string) ([]*ast.Source, error) {
	files := make([]string, 0, 16)
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".graphql") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("gqlplanner: no .graphql files found under %s", root)
	}

	sources := make([]*ast.Source, 0, len(files))
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		sources = append(sources, &ast.Source{
			Name:  f,
			Input: string(b),
		})
	}
	return sources, nil
}
