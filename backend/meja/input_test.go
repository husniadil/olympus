package meja

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// §4.3: a server can open the pipe after the feeder gave up, when the call was
// cancelled before the server reached its load-buffer. Its open waits for a
// writer, and removing the path does not end the wait. The pipe's teardown
// must, and the read it gets must be empty.
func TestAReaderThatOpensThePipeLateGetsAnEmptyRead(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "text")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("making the pipe: %v", err)
	}
	read := make(chan error, 1)
	go func() {
		f, err := os.Open(fifo)
		if err != nil {
			read <- err
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err == nil && len(data) > 0 {
			err = fmt.Errorf("read %q from a pipe nobody fed", data)
		}
		read <- err
	}()
	// Long enough for the open to be waiting on a writer.
	time.Sleep(100 * time.Millisecond)

	release(dir, fifo)
	select {
	case err := <-read:
		if err != nil {
			t.Errorf("the late reader: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a reader that opened the pipe before its teardown is still waiting for a writer")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the pipe's directory outlived its teardown: %v", err)
	}
}
