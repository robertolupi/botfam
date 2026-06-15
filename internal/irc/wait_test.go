package irc

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeLog(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendLog(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestWaitIrcLinesMatchesAndFilters(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")
	writeLog(t, logPath, "12:00 <bob> earlier traffic\n")

	go func() {
		time.Sleep(200 * time.Millisecond)
		appendLog(t, logPath,
			"12:01 (hist) <bob> replayed line\n"+
				"12:01 <alice> my own message\n"+
				"* alice JOIN #botfam\n"+
				"12:02 <bob> hello alice\n")
	}()

	lines, newOffset, timedOut, err := WaitIrcLines(logPath, "alice", -1, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitIrcLines failed: %v", err)
	}
	if timedOut {
		t.Fatal("unexpected timeout")
	}
	want := []string{"12:02 <bob> hello alice"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	stat, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if newOffset != stat.Size() {
		t.Errorf("newOffset = %d, want file size %d", newOffset, stat.Size())
	}
}

func TestWaitIrcLinesFromOffsetReadsExistingContent(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")
	writeLog(t, logPath, "12:00 <bob> already here\n")

	lines, newOffset, timedOut, err := WaitIrcLines(logPath, "alice", 0, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitIrcLines failed: %v", err)
	}
	if timedOut {
		t.Fatal("unexpected timeout")
	}
	want := []string{"12:00 <bob> already here"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	stat, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if newOffset != stat.Size() {
		t.Errorf("newOffset = %d, want file size %d", newOffset, stat.Size())
	}
}

func TestWaitIrcLinesTimeout(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")
	writeLog(t, logPath, "12:00 <bob> static content\n")
	stat, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}

	lines, newOffset, timedOut, err := WaitIrcLines(logPath, "alice", -1, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitIrcLines failed: %v", err)
	}
	if !timedOut {
		t.Fatal("expected timeout")
	}
	if len(lines) != 0 {
		t.Errorf("expected no lines, got %v", lines)
	}
	if newOffset != stat.Size() {
		t.Errorf("newOffset = %d, want snapshot size %d", newOffset, stat.Size())
	}
}

func TestWaitIrcLinesTimeoutMissingFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")

	_, newOffset, timedOut, err := WaitIrcLines(logPath, "alice", -1, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitIrcLines failed: %v", err)
	}
	if !timedOut {
		t.Fatal("expected timeout while waiting for a missing file")
	}
	if newOffset != -1 {
		t.Errorf("newOffset = %d, want -1 (unchanged fromOffset)", newOffset)
	}
}

func TestWaitIrcLinesTruncationReset(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")
	writeLog(t, logPath, "12:00 <bob> line one\n12:00 <bob> line two\n")
	stat, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := os.Truncate(logPath, 0); err != nil {
			t.Error(err)
			return
		}
		// Let at least one poll observe the truncation before new content.
		time.Sleep(1200 * time.Millisecond)
		appendLog(t, logPath, "12:05 <bob> after rotation\n")
	}()

	lines, newOffset, timedOut, err := WaitIrcLines(logPath, "alice", stat.Size(), 15*time.Second)
	if err != nil {
		t.Fatalf("WaitIrcLines failed: %v", err)
	}
	if timedOut {
		t.Fatal("unexpected timeout")
	}
	want := []string{"12:05 <bob> after rotation"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	if newOffset != int64(len("12:05 <bob> after rotation\n")) {
		t.Errorf("newOffset = %d, want %d", newOffset, len("12:05 <bob> after rotation\n"))
	}
}

func TestReadIrcLogTailDefault(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")
	content := "12:00 (hist) <bob> replay\n12:01 <alice> own line\n12:02 <bob> third\n"
	writeLog(t, logPath, content)

	lines, nextOffset, err := ReadIrcLog(logPath, -1, 0)
	if err != nil {
		t.Fatalf("ReadIrcLog failed: %v", err)
	}
	// No nick filtering: raw tail returns every line, including replays.
	want := []string{
		"12:00 (hist) <bob> replay",
		"12:01 <alice> own line",
		"12:02 <bob> third",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	if nextOffset != int64(len(content)) {
		t.Errorf("nextOffset = %d, want file size %d", nextOffset, len(content))
	}
}

func TestReadIrcLogTailMaxLines(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")
	content := "a\nb\nc\nd\n"
	writeLog(t, logPath, content)

	lines, nextOffset, err := ReadIrcLog(logPath, -1, 2)
	if err != nil {
		t.Fatalf("ReadIrcLog failed: %v", err)
	}
	want := []string{"c", "d"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	if nextOffset != int64(len(content)) {
		t.Errorf("nextOffset = %d, want file size %d", nextOffset, len(content))
	}
}

func TestReadIrcLogOffsetPaging(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")
	writeLog(t, logPath, "one\ntwo\nthree\nfour\n")

	lines, nextOffset, err := ReadIrcLog(logPath, 0, 2)
	if err != nil {
		t.Fatalf("ReadIrcLog page 1 failed: %v", err)
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("page 1 = %v, want %v", lines, want)
	}
	if nextOffset != int64(len("one\ntwo\n")) {
		t.Errorf("page 1 nextOffset = %d, want %d", nextOffset, len("one\ntwo\n"))
	}

	lines, nextOffset, err = ReadIrcLog(logPath, nextOffset, 2)
	if err != nil {
		t.Fatalf("ReadIrcLog page 2 failed: %v", err)
	}
	if want := []string{"three", "four"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("page 2 = %v, want %v", lines, want)
	}
	if nextOffset != int64(len("one\ntwo\nthree\nfour\n")) {
		t.Errorf("page 2 nextOffset = %d, want %d", nextOffset, len("one\ntwo\nthree\nfour\n"))
	}

	lines, nextOffset2, err := ReadIrcLog(logPath, nextOffset, 2)
	if err != nil {
		t.Fatalf("ReadIrcLog page 3 failed: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("page 3 = %v, want empty", lines)
	}
	if nextOffset2 != nextOffset {
		t.Errorf("page 3 nextOffset = %d, want unchanged %d", nextOffset2, nextOffset)
	}
}

func TestReadIrcLogOffsetPagingPartialLine(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log")
	// "thr" is a line still being written: no trailing newline yet.
	writeLog(t, logPath, "one\ntwo\nthr")

	lines, nextOffset, err := ReadIrcLog(logPath, 0, 10)
	if err != nil {
		t.Fatalf("ReadIrcLog failed: %v", err)
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v (partial line must not be consumed)", lines, want)
	}
	if nextOffset != int64(len("one\ntwo\n")) {
		t.Errorf("nextOffset = %d, want %d (cursor must stay before the fragment)", nextOffset, len("one\ntwo\n"))
	}

	// Writer completes the line; the next page reads it whole.
	writeLog(t, logPath, "one\ntwo\nthree\n")
	lines, _, err = ReadIrcLog(logPath, nextOffset, 10)
	if err != nil {
		t.Fatalf("ReadIrcLog after completion failed: %v", err)
	}
	if want := []string{"three"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("after completion = %v, want %v", lines, want)
	}
}
