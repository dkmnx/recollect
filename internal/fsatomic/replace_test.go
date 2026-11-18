package fsatomic_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.
import (
	"fmt"
	"github.com/dkmnx/recollect/internal/fsatomic"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A reader that holds the destination open briefly is the shape Windows
// refuses and Unix ignores: Go opens files without FILE_SHARE_DELETE, so the
// replace must ride out the hold instead of losing the save.
func TestReplaceRidesOutABriefOpenHandle(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")
	src := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	held, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		held.Close()
	}()

	if err := fsatomic.Replace(src, dst); err != nil {
		t.Fatalf("replace under a briefly held destination: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("destination = %q, want the new contents", data)
	}
}

// A concurrent reader is the production shape: it opens and closes in a tight
// loop, so the destination is busy intermittently rather than held once.
func TestReplaceSurvivesAConcurrentReader(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(dst, []byte(`{"v":0}`), 0o600); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if f, err := os.Open(dst); err == nil {
				f.Read(make([]byte, 64))
				f.Close()
			}
		}
	}()

	// A dozen rounds is enough to hit the window many times over; each retry
	// costs a backoff sleep, so a long loop buys no extra confidence.
	for i := 1; i <= 12; i++ {
		tmp := filepath.Join(dir, "tmp")
		if err := os.WriteFile(tmp, []byte(fmt.Sprintf(`{"v":%d}`, i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fsatomic.Replace(tmp, dst); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("replace %d under a concurrent reader: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"v":12}`; string(data) != want {
		t.Fatalf("destination = %q, want the last write %q", data, want)
	}
}
