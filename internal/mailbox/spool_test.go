package mailbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesMaildir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool", "claude")
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{boxTmp, boxNew, boxCur} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			t.Errorf("%s/ not created: err=%v", sub, err)
		}
	}
}

func TestDeliverAtomicAndReadAck(t *testing.T) {
	sp, err := Open(filepath.Join(t.TempDir(), "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Deliver(&Message{Source: SourceIRC, Subject: "ping", Body: "claude: ping"}); err != nil {
		t.Fatal(err)
	}

	// tmp/ must be empty after delivery (the rename moved it out atomically).
	if des, _ := os.ReadDir(filepath.Join(sp.Dir(), boxTmp)); len(des) != 0 {
		t.Errorf("tmp/ not empty after deliver: %d files", len(des))
	}

	news, err := sp.ListNew()
	if err != nil {
		t.Fatal(err)
	}
	if len(news) != 1 {
		t.Fatalf("ListNew = %d, want 1", len(news))
	}
	m, err := sp.Read(news[0])
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "ping" || m.Body != "claude: ping" {
		t.Errorf("read back wrong message: %+v", m)
	}

	// Ack moves new/ -> cur/.
	if err := sp.Ack(news[0]); err != nil {
		t.Fatal(err)
	}
	if news, _ := sp.ListNew(); len(news) != 0 {
		t.Errorf("ListNew after ack = %d, want 0", len(news))
	}
	if curs, _ := sp.ListCur(); len(curs) != 1 {
		t.Errorf("ListCur after ack = %d, want 1 (replay buffer)", len(curs))
	}
}

func TestDeliverPreservesOrder(t *testing.T) {
	sp, _ := Open(filepath.Join(t.TempDir(), "claude"))
	const n = 50
	for i := 0; i < n; i++ {
		if _, err := sp.Deliver(&Message{Source: SourceForge, Body: itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	news, err := sp.ListNew()
	if err != nil {
		t.Fatal(err)
	}
	if len(news) != n {
		t.Fatalf("ListNew = %d, want %d", len(news), n)
	}
	for i, e := range news {
		m, _ := sp.Read(e)
		if m.Body != itoa(i) {
			t.Fatalf("entry %d body = %q, want %q (delivery order not preserved)", i, m.Body, itoa(i))
		}
	}
}

func TestAckRejectsCurEntry(t *testing.T) {
	sp, _ := Open(filepath.Join(t.TempDir(), "claude"))
	sp.Deliver(&Message{Source: SourceIRC, Body: "x"})
	news, _ := sp.ListNew()
	if err := sp.Ack(news[0]); err != nil {
		t.Fatal(err)
	}
	curs, _ := sp.ListCur()
	if err := sp.Ack(curs[0]); err == nil {
		t.Error("Ack of a cur/ entry should error (only new/ is ackable)")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
