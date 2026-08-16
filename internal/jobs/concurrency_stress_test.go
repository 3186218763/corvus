package jobs

import (
	"context"
	"io"
	"sync"
	"testing"

	"corvus/internal/event"
)

// TestManagerConcurrentAccess hammers every public Manager method from many
// goroutines. Any map access that escapes m.mu trips the runtime's built-in
// "concurrent map writes/read" fatal even without the race detector.
func TestManagerConcurrentAccess(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	const workers = 24
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch (w + i) % 6 {
				case 0:
					j := m.StartForSession("", "bash", "x", func(ctx context.Context, out io.Writer) (string, error) {
						_, _ = out.Write([]byte("tick"))
						return "done", nil
					})
					_, _, _ = m.OutputForSession("", j.ID)
				case 1:
					_ = m.RunningForSession("")
				case 2:
					_ = m.DrainCompletedNoteForSession("")
				case 3:
					_ = m.WaitForSession(context.Background(), "", nil, 0) // non-blocking-ish: returns running snapshot
				case 4:
					m.KillForSession("", "bash-1")
				case 5:
					_, _, _ = m.OutputForSession("", "bash-2")
				}
			}
		}(w)
	}
	wg.Wait()
}
