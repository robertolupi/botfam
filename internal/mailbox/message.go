// Package mailbox is the per-agent delivery spool that backs `botfam wait`.
//
// A spool is a Maildir (proposal-event-delivery-redesign §3): a directory
//
//	$FAMROOT/spool/$agent/{tmp,new,cur}
//
// Delivery is lock-free: a message is written to tmp/ then atomically renamed
// (os.Rename) into new/ under a unique filename — the actors-not-locks primitive,
// no flock/byte-offset/epoch machinery. The filename is the delivery cursor:
// new/ holds undelivered messages, cur/ holds read ones, and a read is the move
// new/->cur/ (the ack). cur/ doubles as the local replay buffer; age-based
// rotation (#249) is a later milestone.
//
// Each message is an RFC-822-style envelope (headers + body); see [Message]. The
// header values are sanitized on encode (§4) because some — notably Subject —
// are attacker-influenceable (a peer's IRC line, an external PR title) and must
// never be able to forge headers or inject text into a notification channel.
package mailbox

import (
	"bytes"
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

// Source values for [Message.Source].
const (
	SourceIRC   = "irc"
	SourceForge = "forge"
)

// Header names of the §4 envelope. Traceparent is reserved for the cross-harness
// trace carrier (M5 / #309 Part B); this milestone only encodes/decodes it.
const (
	hdrFrom        = "From"
	hdrTo          = "To"
	hdrSubject     = "Subject"
	hdrSource      = "Source"
	hdrKind        = "Kind"
	hdrSeq         = "Seq"
	hdrDate        = "Date"
	hdrDirected    = "Directed"
	hdrTraceparent = "Traceparent"
)

// Length caps for sanitized header values. Subject is the tightest because it is
// the human-facing summary that also feeds the (future) notification nudge.
const (
	maxSubjectLen = 120
	maxNameLen    = 64
	maxValueLen   = 256
)

// Message is one spool message: the §4 RFC-822-style headers plus a body. The
// headers are the contract (the notification nudge is a sanitized projection of
// them; `botfam wait` returns the whole message). All header values are treated
// as untrusted data and sanitized on [Message.Encode]; the body is opaque.
type Message struct {
	From        string    // sender / source identity (actor or channel name)
	To          string    // recipient (mailbox owner / channel)
	Subject     string    // generated human summary (sanitized, capped)
	Source      string    // irc / forge / ...
	Kind        string    // event subtype (drives interrupt-vs-can-wait)
	Seq         int64     // per-source monotonic counter (gap detection)
	Date        time.Time // UTC timestamp; defaults to now on encode if zero
	Directed    bool      // event is addressed to the mailbox owner (assignee/mention) — drives `botfam wait`'s DND default
	Traceparent string    // W3C trace context (reserved, M5)
	Body        string    // full content / URL / payload
}

// Encode renders the message as sanitized headers, a blank line, then the body
// verbatim, using CRLF line endings. Empty headers are omitted; Date defaults to
// the current UTC time when unset. Sanitization (strip CR/LF + control chars,
// length-cap) is applied here so a crafted Subject/From cannot inject extra
// headers or break out of the header block.
func (m *Message) Encode() []byte {
	var b bytes.Buffer
	put := func(k, v string) {
		if v == "" {
			return
		}
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	put(hdrFrom, sanitizeName(m.From))
	put(hdrTo, sanitizeName(m.To))
	put(hdrSubject, sanitizeHeaderValue(m.Subject, maxSubjectLen))
	put(hdrSource, sanitizeHeaderValue(m.Source, maxNameLen))
	put(hdrKind, sanitizeHeaderValue(m.Kind, maxNameLen))
	if m.Seq > 0 {
		put(hdrSeq, strconv.FormatInt(m.Seq, 10))
	}
	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}
	put(hdrDate, date.UTC().Format(time.RFC3339))
	if m.Directed {
		put(hdrDirected, "true")
	}
	put(hdrTraceparent, sanitizeHeaderValue(m.Traceparent, maxValueLen))
	b.WriteString("\r\n")
	b.WriteString(m.Body)
	return b.Bytes()
}

// SanitizedHeaders returns the message's header values after the same
// sanitization Encode applies (CR/LF + control stripping and length caps, §4),
// as a map suitable for the best-effort MCP notification nudge (§5). It carries
// only the metadata that lets an agent judge urgency — From/To/Subject/Source/
// Kind/Seq/Date — and never the body, a URL, or Traceparent.
func (m *Message) SanitizedHeaders() map[string]string {
	h := make(map[string]string, 7)
	set := func(k, v string) {
		if v != "" {
			h[k] = v
		}
	}
	set(hdrFrom, sanitizeName(m.From))
	set(hdrTo, sanitizeName(m.To))
	set(hdrSubject, sanitizeHeaderValue(m.Subject, maxSubjectLen))
	set(hdrSource, sanitizeHeaderValue(m.Source, maxNameLen))
	set(hdrKind, sanitizeHeaderValue(m.Kind, maxNameLen))
	if m.Seq > 0 {
		set(hdrSeq, strconv.FormatInt(m.Seq, 10))
	}
	date := m.Date
	if date.IsZero() {
		date = time.Now()
	}
	set(hdrDate, date.UTC().Format(time.RFC3339))
	return h
}

// ParseMessage decodes an [Message.Encode] envelope using the standard library's
// net/mail RFC-5322 reader. Header lookups are case-insensitive (Header.Get
// canonicalizes); the body is everything after the blank line. Unknown headers
// are ignored and malformed Seq/Date values are skipped (best effort) — a
// message's body is never lost to a header parse miss. Date is read as RFC-3339
// (our on-disk format) rather than via the RFC-5322 Header.Date helper.
func ParseMessage(b []byte) (*Message, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, err
	}
	m := &Message{
		From:        msg.Header.Get(hdrFrom),
		To:          msg.Header.Get(hdrTo),
		Subject:     msg.Header.Get(hdrSubject),
		Source:      msg.Header.Get(hdrSource),
		Kind:        msg.Header.Get(hdrKind),
		Directed:    msg.Header.Get(hdrDirected) == "true",
		Traceparent: msg.Header.Get(hdrTraceparent),
		Body:        string(body),
	}
	if v := msg.Header.Get(hdrSeq); v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			m.Seq = n
		}
	}
	if v := msg.Header.Get(hdrDate); v != "" {
		if ts, perr := time.Parse(time.RFC3339, v); perr == nil {
			m.Date = ts.UTC()
		}
	}
	return m, nil
}

// sanitizeHeaderValue strips CR/LF and other control characters (so a value can
// never forge a header or inject a newline into a channel) and caps the result
// to max runes after trimming surrounding whitespace.
func sanitizeHeaderValue(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\t' {
			b.WriteByte(' ') // fold structural whitespace to a plain space
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue // drop other control characters
		}
		b.WriteRune(r)
	}
	return truncateRunes(strings.TrimSpace(collapseSpaces(b.String())), max)
}

// sanitizeName restricts From/To to actor/channel-name characters, dropping
// anything else, then caps the length. This is stricter than a header value: a
// recipient/sender identity is a name, not free text.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.', r == '#':
			b.WriteRune(r)
		}
	}
	return truncateRunes(b.String(), maxNameLen)
}

// collapseSpaces folds runs of spaces into one, keeping sanitized values compact.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes caps s to at most max runes (not bytes), so multi-byte text is
// never split mid-rune.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
