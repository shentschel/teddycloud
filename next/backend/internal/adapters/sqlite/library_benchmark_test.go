package sqlite

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
)

// Use -run '^$' -bench '^BenchmarkLibraryPages$' -benchtime=30x -count=1
// -benchmem. Each sub-benchmark has five explicit, untimed warmups. Fixture,
// prepare, each advance and first ready page are reported separately. This is
// the uninstrumented latency/allocation companion to the checked profile matrix.
func BenchmarkLibraryPages(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			start := time.Now()
			o, e := OpenLifecycleOwner(b.Context(), Config{Path: filepath.Join(b.TempDir(), "benchmark.sqlite")}, SchemaMigrations())
			if e != nil {
				b.Fatal(e)
			}
			defer o.Close(context.Background())
			tx, e := o.database.db.BeginTx(b.Context(), nil)
			if e != nil {
				b.Fatal(e)
			}
			defer tx.Rollback()
			for i := 0; i < n; i++ {
				owner := catalogSchemaID("cnt", i/4+1)
				if i%4 == 0 {
					if _, e = tx.ExecContext(b.Context(), "INSERT INTO tc_catalog_content VALUES (?,?,NULL,NULL)", owner, "same fingerprint"); e != nil {
						b.Fatal(e)
					}
				}
				if _, e = tx.ExecContext(b.Context(), "INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)", catalogSchemaID("ver", i+1), owner, bytes.Repeat([]byte{1}, 20)); e != nil {
					b.Fatal(e)
				}
			}
			if e = tx.Commit(); e != nil {
				b.Fatal(e)
			}
			b.Logf("fixture items=%d contents=%d construction=%s", n, (n+3)/4, time.Since(start))
			s := lib.NewService(o)
			scope := o.LibraryReadScope()
			start = time.Now()
			p, e := s.Prepare(b.Context(), scope)
			if e != nil {
				b.Fatal(e)
			}
			b.Logf("cold prepare=%s", time.Since(start))
			for i := 0; !p.Ready; i++ {
				start = time.Now()
				p, e = s.Advance(b.Context(), scope)
				if e != nil {
					b.Fatal(e)
				}
				b.Logf("advance=%d elapsed=%s items=%d bytes=%d pages=%d", i, time.Since(start), p.Items, p.Bytes, p.Pages)
			}
			var sqliteVersion string
			if e = o.library.index.QueryRow("SELECT sqlite_version()").Scan(&sqliteVersion); e != nil {
				b.Fatal(e)
			}
			b.Logf("runner=%s/%s go=%s sqlite=%s CPUs=%d schema=10 profile=%s layout=%s cache=new-index/source-OS-uncontrolled", runtime.GOOS, runtime.GOARCH, runtime.Version(), sqliteVersion, runtime.NumCPU(), lib.Profile, libraryIndexLayout)
			start = time.Now()
			if _, e = s.ListLibrary(b.Context(), lib.Query{}, lib.Page{Limit: 1}, scope); e != nil {
				b.Fatal(e)
			}
			b.Logf("first-ready-page=%s", time.Since(start))
			for _, order := range []string{lib.IdentityAsc, lib.TitleAsc, lib.TitleDesc} {
				for _, limit := range []uint64{1, 16, 128} {
					for _, continuation := range []bool{false, true} {
						b.Run(fmt.Sprintf("%s/%s/%d/continuation-%v", runtime.GOOS, order, limit, continuation), func(b *testing.B) {
							q := lib.Query{Versions: lib.All, Sort: order}
							page := lib.Page{Limit: limit}
							if continuation {
								first, e := s.ListLibrary(b.Context(), q, page, scope)
								if e != nil || first.Next == "" {
									b.Fatal("continuation setup", e)
								}
								page.Cursor = first.Next
								page.ExpectedSnapshot = first.Snapshot
							}
							for i := 0; i < 5; i++ {
								if _, e := s.ListLibrary(b.Context(), q, page, scope); e != nil {
									b.Fatal(e)
								}
							}
							b.ReportAllocs()
							b.ResetTimer()
							for i := 0; i < b.N; i++ {
								r, e := s.ListLibrary(b.Context(), q, page, scope)
								if e != nil || r.Inspected > limit+1 {
									b.Fatal(e, r.Inspected)
								}
							}
						})
					}
				}
			}
		})
	}
}
