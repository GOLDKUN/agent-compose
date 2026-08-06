package runs

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestProductionCodeDoesNotDependOnTransportPackages(t *testing.T) {
	t.Parallel()

	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve dependency contract source path")
	}
	packageDir := filepath.Dir(sourcePath)
	files, err := filepath.Glob(filepath.Join(packageDir, "*.go"))
	if err != nil {
		t.Fatalf("list package files: %v", err)
	}

	forbidden := []string{"net/http", "connectrpc.com/connect"}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Base(path), err)
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", filepath.Base(path), err)
			}
			for _, prefix := range forbidden {
				if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
					t.Errorf("%s imports transport package %q", filepath.Base(path), importPath)
				}
			}
		}
	}
}
