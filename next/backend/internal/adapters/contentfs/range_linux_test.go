//go:build linux

package contentfs

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"golang.org/x/sys/unix"
)

type rangeSink struct {
	bytes.Buffer
	write  func(context.Context, []byte) (int, error)
	closed chan struct{}
	once   sync.Once
}

func (s *rangeSink) Write(ctx context.Context, b []byte) (int, error) {
	if s.write != nil {
		return s.write(ctx, b)
	}
	return s.Buffer.Write(b)
}
func (s *rangeSink) Close() error {
	s.once.Do(func() {
		if s.closed != nil {
			close(s.closed)
		}
	})
	return nil
}
func requested(t *testing.T, offset, length int64) ByteRange {
	t.Helper()
	r, err := NewByteRange(offset, length)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBlobVerifiedRanges(t *testing.T) {
	s, root := newStore(t)
	const capBytes = 65536
	s.setRangeOptions(RangeOptions{capBytes, DefaultRangeDuration})
	f := fixture(t, capBytes+1)
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal(err)
	}
	id, size := content.NewBlobID(f.BlobDigest()), f.CompleteBytes()
	for _, tc := range []struct {
		name           string
		offset, length int64
		want           error
	}{
		{"exact", 4096, capBytes, nil},
		{"one-over", 4096, capBytes + 1, ErrInvalid},
		{"EOF-empty", int64(size), 0, nil},
		{"past-EOF", int64(size), 1, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &rangeSink{}
			err := s.ReadRange(context.Background(), id, size, requested(t, tc.offset, tc.length), sink)
			if err != tc.want {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if err == nil {
				want := allBytes(t, f)[tc.offset : tc.offset+tc.length]
				if !bytes.Equal(sink.Bytes(), want) {
					t.Fatal("incorrect complete-file range")
				}
			} else if sink.Len() != 0 {
				t.Fatal("output before rejection")
			}
		})
	}
	path := blobPath(root, f)
	original := allBytes(t, f)
	mutated := append([]byte(nil), original...)
	mutated[len(mutated)-1] ^= 1
	if err := os.WriteFile(path, mutated, 0600); err != nil {
		t.Fatal(err)
	}
	sink := &rangeSink{}
	if err := s.ReadRange(context.Background(), id, size, requested(t, int64(size), 0), sink); err != ErrCorrupt || sink.Len() != 0 {
		t.Fatal("same-size corruption not detected", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadRange(context.Background(), id, size, requested(t, 0, 1), sink); err != ErrMissing {
		t.Fatal("missing", err)
	}
}

func TestBlobRangeLimits(t *testing.T) {
	for _, tc := range []struct {
		bytes    uint64
		duration time.Duration
		valid    bool
	}{
		{DefaultRangeBytes, DefaultRangeDuration, true}, {HardRangeBytes, HardRangeDuration, true},
		{0, time.Second, false}, {HardRangeBytes + 1, time.Second, false},
		{1, 0, false}, {1, -1, false}, {1, HardRangeDuration + 1, false},
	} {
		_, err := NewRangeOptions(tc.bytes, tc.duration)
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
	for _, tc := range [][2]int64{{-1, 0}, {0, -1}, {math.MaxInt64, 1}} {
		if _, err := NewByteRange(tc[0], tc[1]); err != ErrInvalid {
			t.Fatal("arithmetic accepted", err)
		}
	}
	s, _ := newStore(t)
	f := fixture(t, 8)
	if _, err := publishFixture(s, f); err != nil {
		t.Fatal(err)
	}
	s.setRangeOptions(RangeOptions{1, time.Second})
	if err := s.ReadRange(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), requested(t, 0, 2), &rangeSink{}); err != ErrInvalid {
		t.Fatal(err)
	}
}

func TestBlobRangeDeliveryFailures(t *testing.T) {
	for _, mode := range []string{"short-read", "short-write", "mutation", "canceled-sink", "blocked-sink", "deadline-sink"} {
		t.Run(mode, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 100)
			if _, err := publishFixture(s, f); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sink := &rangeSink{closed: make(chan struct{})}
			want := ErrUnavailable
			switch mode {
			case "short-read":
				s.ops.pread = func(int, []byte, int64) (int, error) { return 0, nil }
			case "short-write":
				sink.write = func(context.Context, []byte) (int, error) { return 0, errors.New("private path diagnostic") }
			case "mutation":
				sink.write = func(_ context.Context, b []byte) (int, error) {
					file, err := os.OpenFile(blobPath(root, f), os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, err = file.WriteAt([]byte{42}, 4096)
					if err != nil {
						t.Fatal(err)
					}
					if err = file.Close(); err != nil {
						t.Fatal(err)
					}
					return len(b), nil
				}
			case "canceled-sink":
				want = ErrCanceled
				sink.write = func(context.Context, []byte) (int, error) { cancel(); return 0, context.Canceled }
			case "blocked-sink", "deadline-sink":
				want = ErrCanceled
				if mode == "deadline-sink" {
					var c context.CancelFunc
					ctx, c = context.WithTimeout(ctx, 20*time.Millisecond)
					defer c()
				}
				sink.write = func(c context.Context, _ []byte) (int, error) {
					if mode == "blocked-sink" {
						cancel()
					}
					select {
					case <-sink.closed:
						return 0, c.Err()
					case <-time.After(time.Second):
						t.Error("Close did not unblock sink")
						return 0, errors.New("timeout")
					}
				}
			}
			if err := s.ReadRange(ctx, content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), requested(t, 4096, 10), sink); err != want {
				t.Fatalf("got %v want %v", err, want)
			}
			select {
			case <-sink.closed:
			default:
				t.Fatal("sink escaped scope")
			}
		})
	}
}

func TestBlobRangeConfinement(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "FIFO", "wrong-size"} {
		t.Run(kind, func(t *testing.T) {
			s, root := newStore(t)
			f := fixture(t, 100)
			if _, err := publishFixture(s, f); err != nil {
				t.Fatal(err)
			}
			path := blobPath(root, f)
			switch kind {
			case "hardlink":
				if err := os.Link(path, path+".alias"); err != nil {
					t.Fatal(err)
				}
			case "wrong-size":
				if err := os.Truncate(path, 4097); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "FIFO" {
					if err := unix.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					outside := root + "/outside"
					if err := os.WriteFile(outside, allBytes(t, f), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			sink := &rangeSink{}
			if err := s.ReadRange(context.Background(), content.NewBlobID(f.BlobDigest()), f.CompleteBytes(), requested(t, 0, 1), sink); err == nil || sink.Len() != 0 {
				t.Fatal("confinement failed", err)
			}
		})
	}
}
