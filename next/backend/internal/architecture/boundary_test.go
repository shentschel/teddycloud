package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDomainDoesNotImportAdaptersOrTransports(t *testing.T) {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate architecture test")
	}
	domainRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "domain"))
	forbidden := []string{
		"database/sql",
		"io/fs",
		"log",
		"log/slog",
		"net/http",
		"os",
		"path",
		"path/filepath",
		"github.com/shentschel/teddycloud/next/backend/internal/adapters",
	}
	const domainPrefix = "github.com/shentschel/teddycloud/next/backend/internal/domain/"

	err := filepath.WalkDir(domainRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			for _, prefix := range forbidden {
				if name == prefix || strings.HasPrefix(name, prefix+"/") {
					t.Errorf("domain file %s imports forbidden package %s", path, name)
				}
			}
			firstSegment := strings.SplitN(name, "/", 2)[0]
			if strings.Contains(firstSegment, ".") && !strings.HasPrefix(name, domainPrefix) {
				t.Errorf("domain file %s imports non-domain or third-party package %s", path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplicationDoesNotImportPersistenceImplementations(t *testing.T) {
	backendRoot := backendSourceRoot(t)
	assertForbiddenImports(
		t,
		filepath.Join(backendRoot, "internal", "application"),
		[]string{
			"database/sql",
			"modernc.org/sqlite",
			"github.com/shentschel/teddycloud/next/backend/internal/adapters",
		},
		nil,
	)
}

func TestDirectDatabaseOwnershipRemainsInSQLiteAdapter(t *testing.T) {
	backendRoot := backendSourceRoot(t)
	sqliteRoot := filepath.Join(backendRoot, "internal", "adapters", "sqlite")
	assertForbiddenImports(
		t,
		backendRoot,
		[]string{"database/sql", "modernc.org/sqlite"},
		func(path string) bool {
			relative, err := filepath.Rel(sqliteRoot, path)
			return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
		},
	)
}

func backendSourceRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate architecture test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func assertForbiddenImports(
	t *testing.T,
	root string,
	forbidden []string,
	allowed func(string) bool,
) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || allowed != nil && allowed(path) {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			for _, prefix := range forbidden {
				if name == prefix || strings.HasPrefix(name, prefix+"/") {
					t.Errorf("file %s imports forbidden persistence package %s", path, name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// This is a source inventory, not a claim that trusted in-process Go code is a
// security sandbox. The dynamic audit separately exercises the actual ports.
func TestAssignmentAuditNoBypass(t *testing.T) {
	root := backendSourceRoot(t)
	calls := map[string]map[string]bool{
		"WithinAssignmentTransaction": {"internal/application/assignment/service.go": true},
		"AdmitClassification":         {"internal/domain/assignment/classification.go": true, "internal/application/assignment/service.go": true, "internal/application/assignment/queries.go": true, "internal/adapters/sqlite/assignment_repository.go": true, "internal/domain/migration/migration.go": true},
		"ResolveClassification":       {"internal/application/assignment/service.go": true, "internal/application/assignment/queries.go": true, "internal/adapters/sqlite/assignment_repository.go": true},
	}
	seen := map[string]int{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.SelectorExpr:
				if allowed, ok := calls[n.Sel.Name]; ok {
					// Includes method values as well as direct calls, preventing trivial aliases.
					seen[n.Sel.Name]++
					if !allowed[rel] {
						t.Errorf("assignment authority reference %s in %s", n.Sel.Name, rel)
					}
				}
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(n.Value)
				if err != nil {
					return true
				}
				if strings.Contains(value, "tc_assignment") {
					// Every current table-bearing statement, including dynamic table names,
					// is inventoried. Schema owns DDL, repository DML, transaction restore read.
					if rel != "internal/adapters/sqlite/assignment_schema.go" && rel != "internal/adapters/sqlite/assignment_repository.go" && rel != "internal/adapters/sqlite/assignment_transaction.go" {
						t.Errorf("assignment SQL escaped owner: %s", rel)
					}
					if rel == "internal/adapters/sqlite/assignment_transaction.go" && !strings.HasPrefix(strings.TrimSpace(value), "SELECT ") {
						t.Errorf("transaction layer gained assignment write: %s", value)
					}
				}
			}
			return true
		})
		// URI admission and classification cannot acquire network/filesystem/process
		// I/O through their own imports. Existing domain boundary covers dependencies.
		if strings.HasPrefix(rel, "internal/domain/assignment/") || strings.HasPrefix(rel, "internal/application/assignment/") || rel == "internal/domain/migration/migration.go" {
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				for _, prefix := range []string{"net", "os", "syscall", "unsafe", "plugin", "database", "io/fs"} {
					if name == prefix || strings.HasPrefix(name, prefix+"/") {
						t.Errorf("assignment policy gained I/O import %s in %s", name, rel)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The fifth ResolveClassification selector is the already-accepted command
	// kind in the SQLite event mapping, not a new authority call.
	if seen["WithinAssignmentTransaction"] != 1 || seen["AdmitClassification"] != 4 || seen["ResolveClassification"] != 5 {
		t.Fatalf("authority inventory changed: %v", seen)
	}
}

func TestEventApplicationBoundary(t *testing.T) {
	root := backendSourceRoot(t)
	assertForbiddenImports(t, filepath.Join(root, "internal", "application", "eventhub"), []string{"database/sql", "modernc.org/sqlite", "net/http", "os", "github.com/shentschel/teddycloud/next/backend/internal/adapters"}, nil)
	// Internal composition remains unactivated. Runtime ingress must be added by
	// a separately reviewed job, not by importing a permissive listener here.
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "internal/adapters/sqlite/") || strings.HasPrefix(relative, "internal/application/eventhub/") {
			return nil
		}
		file, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if e != nil {
			return e
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "NewEventHub", "AcquireEventObservation", "ObserveEvent", "EventReadCapability":
				t.Errorf("event activation outside its internal owner in %s", relative)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
