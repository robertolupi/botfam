package ops

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestParseChannels(t *testing.T) {
	tests := []struct {
		input        string
		fallback     string
		wantChannels []string
		wantPrimary  string
	}{
		{
			input:        "#botfam",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
		{
			input:        "#botfam,#ccrep",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam", "#ccrep"},
			wantPrimary:  "#botfam",
		},
		{
			input:        " #botfam ,  #ccrep ",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam", "#ccrep"},
			wantPrimary:  "#botfam",
		},
		{
			input:        "",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
		{
			input:        ",,",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
		{
			input:        "",
			fallback:     "#deep-cuts",
			wantChannels: []string{"#deep-cuts"},
			wantPrimary:  "#deep-cuts",
		},
	}

	for _, tt := range tests {
		gotChannels, gotPrimary := ParseChannels(tt.input, tt.fallback)
		if !reflect.DeepEqual(gotChannels, tt.wantChannels) {
			t.Errorf("ParseChannels(%q, %q) gotChannels = %v, want %v", tt.input, tt.fallback, gotChannels, tt.wantChannels)
		}
		if gotPrimary != tt.wantPrimary {
			t.Errorf("ParseChannels(%q, %q) gotPrimary = %q, want %q", tt.input, tt.fallback, gotPrimary, tt.wantPrimary)
		}
	}
}

// TestEmitterConcurrentWrites drives the emitter from many goroutines at once,
// mirroring the FIFO-reader + socket-reader contention in runIrcClient. The
// destinations are plain (unsynchronized) bytes.Buffers, so it is the emitter's
// own lock that must serialize access: under -race this fails if the lock is
// removed (the data race in issue #75) and passes with it. Functionally it also
// asserts no log line is torn or interleaved.
func TestEmitterConcurrentWrites(t *testing.T) {
	logBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}
	e := &emitter{logFile: logBuf, out: outBuf}

	const writers = 16
	const perWriter = 64

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				e.emit(fmt.Sprintf("w%02d-msg%03d", w, i))
			}
		}(w)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(logBuf.String(), "\n"), "\n")
	if len(lines) != writers*perWriter {
		t.Fatalf("expected %d log lines, got %d", writers*perWriter, len(lines))
	}
	// Every line must be a single, well-formed "[stamp] wWW-msgNNN" record —
	// no interleaving from a concurrent writer mid-line.
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "[") || !strings.Contains(ln, "] w") {
			t.Fatalf("torn or malformed log line: %q", ln)
		}
		if strings.Count(ln, "msg") != 1 {
			t.Fatalf("interleaved log line: %q", ln)
		}
	}
	// logFile and out must receive identical streams.
	if logBuf.String() != outBuf.String() {
		t.Errorf("logFile and out diverged:\nlog:\n%s\nout:\n%s", logBuf.String(), outBuf.String())
	}
}
