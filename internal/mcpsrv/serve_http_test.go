package mcpsrv

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf collects the startup lines while ServeHTTP runs in its own
// goroutine.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServeHTTPAnnouncesTheBoundAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out syncBuf
	done := make(chan error, 1)
	go func() { done <- ServeHTTP(ctx, "127.0.0.1:0", Options{Version: "test", Notify: &out}) }()

	addr := regexp.MustCompile(`http://127\.0\.0\.1:\d+`)
	var line string
	for range 100 {
		if line = addr.FindString(out.String()); line != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if line == "" {
		t.Fatalf("no address line within a second; got %q", out.String())
	}
	if !strings.Contains(out.String(), "no authentication") {
		t.Errorf("startup lines omit the auth warning: %q", out.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
}

func TestServeHTTPStaysSilentWithoutNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeHTTP(ctx, "127.0.0.1:0", Options{Version: "test"}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
}

func TestServeHTTPReportsABusyPort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var first syncBuf
	up := make(chan error, 1)
	go func() { up <- ServeHTTP(ctx, "127.0.0.1:0", Options{Version: "test", Notify: &first}) }()

	addr := regexp.MustCompile(`127\.0\.0\.1:\d+`)
	var bound string
	for range 100 {
		if bound = addr.FindString(first.String()); bound != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if bound == "" {
		t.Fatalf("first server never reported an address; got %q", first.String())
	}

	if err := ServeHTTP(context.Background(), bound, Options{Version: "test"}); err == nil {
		t.Fatal("second ServeHTTP on the same port returned no error")
	}

	cancel()
	<-up
}
