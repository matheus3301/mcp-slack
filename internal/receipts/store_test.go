package receipts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}
func TestReceiptDurabilityConflictAndReplay(t *testing.T) {
	s, path := testStore(t)
	digest := Digest([]byte("private body"))
	r, done, e := s.Begin("team:bot:post", "key", digest)
	if e != nil || done {
		t.Fatalf("begin=%+v %v", r, e)
	}
	restarted, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	if _, _, e = restarted.Begin("team:bot:post", "key", digest); !errors.Is(e, ErrUncertain) {
		t.Fatalf("crash did not preserve claim: %v", e)
	}
	if _, _, e = restarted.Begin("team:bot:post", "key", Digest([]byte("changed"))); !errors.Is(e, ErrConflict) {
		t.Fatalf("conflict=%v", e)
	}
	result := Receipt{ChannelID: "C01234567", TS: "1790000000.123456"}
	if e = s.Complete("team:bot:post", "key", digest, result); e != nil {
		t.Fatal(e)
	}
	r, done, e = restarted.Begin("team:bot:post", "key", digest)
	if e != nil || !done || r != result {
		t.Fatalf("replay=%+v %t %v", r, done, e)
	}
	for _, namespace := range []string{"other-team:bot:post", "team:other-bot:post", "team:bot:schedule"} {
		if _, done, e = s.Begin(namespace, "key", digest); e != nil || done {
			t.Fatal("cross-identity receipt collision")
		}
	}
	entries, _ := os.ReadDir(path)
	for _, entry := range entries {
		p := filepath.Join(path, entry.Name())
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "private body") {
			t.Fatal("message body persisted")
		}
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0600 {
			t.Fatal("insecure receipt mode")
		}
	}
}
func TestReceiptConcurrentClaim(t *testing.T) {
	_, path := testStore(t)
	var sends atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			s, e := Open(path)
			if e != nil {
				t.Error(e)
				return
			}
			defer s.Close()
			_, done, e := s.Begin("namespace", "key", "digest")
			if e == nil && !done {
				sends.Add(1)
			}
		})
	}
	wg.Wait()
	if sends.Load() != 1 {
		t.Fatalf("claims=%d", sends.Load())
	}
}
func TestReceiptStorageDenials(t *testing.T) {
	parent := t.TempDir()
	bad := filepath.Join(parent, "bad")
	if e := os.Mkdir(bad, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(bad); e == nil {
		t.Fatal("insecure directory accepted")
	}
	target := filepath.Join(parent, "target")
	s, e := Open(target)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	link := filepath.Join(parent, "link")
	if e = os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	if _, e = Open(link); e == nil {
		t.Fatal("symlink state dir accepted")
	}
	name := Digest([]byte("namespace\x00key"))
	if e = os.WriteFile(filepath.Join(target, "bad.json"), []byte(`{"digest":"digest"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("bad.json", filepath.Join(target, name+".done")); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Begin("namespace", "key", "digest"); e == nil {
		t.Fatal("symlink receipt accepted")
	}
	if e = os.Remove(filepath.Join(target, name+".done")); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(target, name+".done"), []byte(`{`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Begin("namespace", "key", "digest"); e == nil {
		t.Fatal("torn completion retried")
	}
}
