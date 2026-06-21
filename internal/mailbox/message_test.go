package mailbox

import (
	"strings"
	"testing"
	"time"
)

const SourceIRC = "irc"

func TestMessageRoundTrip(t *testing.T) {
	want := &Message{
		From:        "agy",
		To:          "claude",
		Subject:     `review_request: PR #341 "feat: x"`,
		Source:      SourceForge,
		Kind:        "review_request",
		Seq:         7,
		Date:        time.Date(2026, 6, 15, 19, 53, 0, 0, time.UTC),
		Traceparent: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		Body:        "http://gitea:3000/botfam/botfam/pulls/341\n\nfull body with blank lines",
	}
	got, err := ParseMessage(want.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.From != want.From || got.To != want.To || got.Subject != want.Subject {
		t.Errorf("identity/subject mismatch: %+v", got)
	}
	if got.Source != want.Source || got.Kind != want.Kind || got.Seq != want.Seq {
		t.Errorf("source/kind/seq mismatch: %+v", got)
	}
	if !got.Date.Equal(want.Date) {
		t.Errorf("date = %v, want %v", got.Date, want.Date)
	}
	if got.Traceparent != want.Traceparent {
		t.Errorf("traceparent = %q, want %q", got.Traceparent, want.Traceparent)
	}
	if got.Body != want.Body {
		t.Errorf("body = %q, want %q", got.Body, want.Body)
	}
}

func TestEncodeDefaultsDate(t *testing.T) {
	m := &Message{Source: SourceIRC, Subject: "hi"}
	got, err := ParseMessage(m.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.Date.IsZero() {
		t.Error("Date not defaulted on encode")
	}
}

func TestEncodeOmitsEmptyHeaders(t *testing.T) {
	m := &Message{Source: SourceIRC, Subject: "hi"}
	out := string(m.Encode())
	for _, h := range []string{"From:", "To:", "Kind:", "Seq:", "Traceparent:"} {
		if strings.Contains(out, h) {
			t.Errorf("empty header %q was emitted:\n%s", h, out)
		}
	}
	if !strings.Contains(out, "Source: irc") || !strings.Contains(out, "Subject: hi") {
		t.Errorf("required headers missing:\n%s", out)
	}
}

// TestSanitizeSubjectStripsInjection is the security case from the issue: a
// Subject crafted to forge extra headers / inject newlines must be neutralized.
func TestSanitizeSubjectStripsInjection(t *testing.T) {
	evil := "innocent\r\nTo: victim\r\nX-Injected: pwned\nmore"
	m := &Message{Source: SourceIRC, Subject: evil}
	enc := m.Encode()

	// The crafted CR/LF must be folded so the value stays on a single Subject
	// line — it must never create a second header line in the encoded block.
	headerBlock, _, _ := strings.Cut(string(enc), "\r\n\r\n")
	for _, line := range strings.Split(headerBlock, "\r\n") {
		if strings.HasPrefix(line, "To:") || strings.HasPrefix(line, "X-Injected:") {
			t.Errorf("header injection forged a header line: %q\nfull:\n%s", line, enc)
		}
	}

	got, err := ParseMessage(enc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got.Subject, "\r\n") {
		t.Errorf("decoded Subject still contains CR/LF: %q", got.Subject)
	}
	// The "To:" the attacker tried to smuggle must NOT have become the message To.
	if got.To == "victim" {
		t.Errorf("header injection forged a To header: %q", got.To)
	}
}

func TestSubjectLengthCap(t *testing.T) {
	long := strings.Repeat("A", 500)
	m := &Message{Source: SourceIRC, Subject: long}
	got, _ := ParseMessage(m.Encode())
	if len([]rune(got.Subject)) != maxSubjectLen {
		t.Errorf("Subject length = %d, want cap %d", len([]rune(got.Subject)), maxSubjectLen)
	}
}

func TestSanitizeNameDropsInvalidChars(t *testing.T) {
	m := &Message{From: "ag y!\r\n<evil>", To: "#bot fam", Source: SourceIRC}
	got, _ := ParseMessage(m.Encode())
	if got.From != "agyevil" {
		t.Errorf("From = %q, want sanitized to actor-name chars", got.From)
	}
	if got.To != "#botfam" {
		t.Errorf("To = %q, want sanitized channel name", got.To)
	}
}

func TestParseToleratesUnknownAndMalformed(t *testing.T) {
	raw := "Source: irc\r\nX-Unknown: whatever\r\nSeq: not-a-number\r\nSubject: ok\r\n\r\nbody"
	got, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "irc" || got.Subject != "ok" || got.Body != "body" {
		t.Errorf("lenient parse dropped good fields: %+v", got)
	}
	if got.Seq != 0 {
		t.Errorf("malformed Seq should be skipped, got %d", got.Seq)
	}
}

func TestSanitizedHeadersOmitsBodyAndSanitizes(t *testing.T) {
	m := &Message{
		From:    "forge",
		To:      "#botfam",
		Subject: "issue: botfam/botfam#1 \"hi\"\r\nInjected: evil",
		Source:  SourceForge,
		Kind:    "issue",
		Seq:     7,
		Body:    "http://gitea/secret-url",
	}
	h := m.SanitizedHeaders()
	if _, ok := h["Body"]; ok {
		t.Error("SanitizedHeaders must never include the body")
	}
	for _, v := range h {
		if strings.ContainsAny(v, "\r\n") {
			t.Errorf("header value not sanitized (CR/LF present): %q", v)
		}
	}
	if h["Source"] != "forge" || h["Kind"] != "issue" || h["Seq"] != "7" {
		t.Errorf("unexpected headers: %+v", h)
	}
	// CR/LF stripping (no header forging) is the security property checked above;
	// the literal text survives harmlessly as Subject content, which is fine.
	if h["Subject"] == "" {
		t.Error("Subject should be present (sanitized, not dropped)")
	}
}
