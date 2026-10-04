//go:build linux

package contentfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"golang.org/x/sys/unix"
)

func TestBlobCapabilityFailures(t *testing.T) {
	t.Run("actual-cross-device-traversal", func(t *testing.T) {
		fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		child, err := (syscalls{}).open(fd, "proc", unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if child >= 0 {
			unix.Close(child)
		}
		if err != unix.EXDEV {
			t.Fatal("NO_XDEV failed on proc mount", err)
		}
	})
	t.Run("actual-device-descriptor", func(t *testing.T) {
		s, _ := newStore(t)
		fd, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		if _, err := s.checkFile(fd, -1); err != ErrCorrupt {
			t.Fatal("device file accepted", err)
		}
	})
	t.Run("actual-nonlocal-filesystem", func(t *testing.T) {
		fd, err := unix.Open("/proc", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		if err := qualifyFilesystem(fd, syscalls{}); err != ErrUnsupported {
			t.Fatal("procfs admitted", err)
		}
	})
	t.Run("descriptor-traversal-denied", func(t *testing.T) {
		s, _ := newStore(t)
		for _, name := range []string{"../outside", "/proc/self/fd/0", "../../etc/passwd"} {
			fd, err := s.ops.open(s.root, name, unix.O_RDONLY|unix.O_NONBLOCK, 0)
			if fd >= 0 {
				unix.Close(fd)
				t.Fatal("unsafe name opened")
			}
			if err == nil {
				t.Fatal("path not denied")
			}
		}
	})
	for _, point := range []string{"filesystem", "openat2", "probe-case", "probe-file-sync", "probe-dir-sync", "probe-cleanup-sync", "lock-sync"} {
		t.Run(point, func(t *testing.T) {
			root := qualifiedRoot(t)
			ops := syscalls{fault: func(p string) error {
				if p == point {
					return unix.ENOSYS
				}
				return nil
			}}
			if s, err := openWithSyscalls(context.Background(), root, content.DefaultTAFOptions(), ops); err != ErrUnsupported {
				if s != nil {
					s.Close()
				}
				t.Fatal("capability admitted", err)
			}
		})
	}
	for _, fault := range []error{unix.EXDEV, unix.ENOSYS, unix.EINVAL, unix.EOPNOTSUPP} {
		t.Run("rename-unavailable", func(t *testing.T) {
			root := qualifiedRoot(t)
			ops := syscalls{fault: func(p string) error {
				if p == "renameat2" {
					return fault
				}
				return nil
			}}
			if s, err := openWithSyscalls(context.Background(), root, content.DefaultTAFOptions(), ops); err != ErrUnsupported {
				if s != nil {
					s.Close()
				}
				t.Fatal(err)
			}
		})
	}
	t.Run("second-owner", func(t *testing.T) {
		s, root := newStore(t)
		if second, err := Open(context.Background(), root, content.DefaultTAFOptions()); err != ErrBusy {
			if second != nil {
				second.Close()
			}
			t.Fatal(err)
		}
		s.Close()
		second, err := Open(context.Background(), root, content.DefaultTAFOptions())
		if err != nil {
			t.Fatal("lock not released", err)
		}
		second.Close()
	})
	t.Run("root-mode", func(t *testing.T) {
		root := qualifiedRoot(t)
		os.Chmod(root, 0777)
		if s, err := Open(context.Background(), root, content.DefaultTAFOptions()); err != ErrUnsupported {
			if s != nil {
				s.Close()
			}
			t.Fatal(err)
		}
	})
	t.Run("lock-hardlink", func(t *testing.T) {
		root := qualifiedRoot(t)
		path := filepath.Join(root, ".owner.lock")
		os.WriteFile(path, nil, 0600)
		os.Link(path, filepath.Join(root, "another"))
		if s, err := Open(context.Background(), root, content.DefaultTAFOptions()); err != ErrUnsupported {
			if s != nil {
				s.Close()
			}
			t.Fatal(err)
		}
	})
	t.Run("retained-probe-remnants", func(t *testing.T) {
		root := qualifiedRoot(t)
		ops := syscalls{fault: func(p string) error {
			if p == "probe-dir-sync" {
				return unix.EIO
			}
			return nil
		}}
		s, err := openWithSyscalls(context.Background(), root, content.DefaultTAFOptions(), ops)
		if s != nil || err != ErrUnsupported {
			t.Fatal(err)
		}
		if countStages(t, root) == 0 {
			t.Fatal("failed probe removed remnants")
		}
		if s, err = Open(context.Background(), root, content.DefaultTAFOptions()); s != nil || err != ErrRetained {
			t.Fatal("remnants ignored", err)
		}
	})
	t.Run("publish-exdev-no-fallback", func(t *testing.T) {
		s, root := newStore(t)
		f := fixture(t, 1)
		s.ops.fault = func(p string) error {
			if p == "renameat2" {
				return unix.EXDEV
			}
			return nil
		}
		if _, err := publishFixture(s, f); err != ErrUnavailable {
			t.Fatal(err)
		}
		assertNoBlob(t, root, f)
		if countStages(t, root) != 1 {
			t.Fatal("stage moved/copied")
		}
	})
	t.Run("invalid-root-paths", func(t *testing.T) {
		for _, root := range []string{"relative", "/", "/tmp/../tmp", "/tmp//no", "/tmp/zero\x00"} {
			if s, err := Open(context.Background(), root, content.DefaultTAFOptions()); s != nil || err != ErrUnsupported {
				t.Fatal(root, err)
			}
		}
	})
}
