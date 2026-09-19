package herdr

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

// The walk and the attachment's cleanup can both release the walk lock, and
// from different goroutines: a client that exits mid-walk has its cleanup run
// while the walk is still returning. Releasing it twice at once is safe.
func TestTheWalkLockReleasesSafelyFromTwoGoroutines(t *testing.T) {
	lock, err := acquireWalkLock(context.Background(), filepath.Join(t.TempDir(), "s.sock"))
	if err != nil {
		t.Fatalf("acquireWalkLock: %v", err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock.release()
		}()
	}
	wg.Wait()
}
