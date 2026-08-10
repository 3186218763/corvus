package control

import (
	"log/slog"
	"time"
)

// launchBackgroundCommand runs body on a tracked background goroutine unless
// the controller is already closed. The closed check and the WaitGroup Add are
// atomic under c.mu, so a Submit racing Close can never Add to bgWG while
// Close is waiting on it (WaitGroup misuse).
func (c *Controller) launchBackgroundCommand(body func()) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.bgWG.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.bgWG.Done()
		body()
	}()
}

// bgCommandGrace bounds Close's wait for in-flight background slash commands.
// Commands observe cancellation promptly; the bound only fires when one ignores
// it. Kept as a package variable so tests can shrink it.
var bgCommandGrace = 15 * time.Second

func (c *Controller) waitForBackgroundCommands() {
	done := make(chan struct{})
	go func() {
		c.bgWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(bgCommandGrace):
		slog.Warn("controller: background slash commands did not exit within grace", "grace", bgCommandGrace)
	}
}
