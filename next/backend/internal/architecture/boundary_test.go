package architecture_test

import (
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
