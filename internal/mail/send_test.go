package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP speaks just enough SMTP for one delivery and returns what it got.
func fakeSMTP(t *testing.T) (addr string, got <-chan string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	out := make(chan string, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r, w := bufio.NewReader(conn), conn
		say := func(line string) { _, _ = w.Write([]byte(line + "\r\n")) }
		say("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					say("250 queued")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				say("250 ok")
			case cmd == "DATA":
				inData = true
				say("354 go ahead")
			case cmd == "QUIT":
				say("221 bye")
				out <- data.String()
				return
			default:
				say("502 unknown")
			}
		}
	}()
	return l.Addr().String(), out
}

// TestSendMail_Delivers: the context-aware send speaks SMTP like
// smtp.SendMail did.
func TestSendMail_Delivers(t *testing.T) {
	t.Parallel()
	addr, got := fakeSMTP(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sendMail(ctx, addr, "127.0.0.1", nil, "baleia@x.io", "ana@x.io", []byte("Subject: hi\r\n\r\nhello\r\n")); err != nil {
		t.Fatal(err)
	}
	if body := <-got; !strings.Contains(body, "hello") {
		t.Errorf("delivered %q, want the message", body)
	}
}

// TestSendMail_StalledServerHonorsTheContext: a server that accepts and
// never answers no longer holds the delivery past its context (#422).
func TestSendMail_StalledServerHonorsTheContext(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		conn, err := l.Accept()
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := sendMail(ctx, l.Addr().String(), "127.0.0.1", nil, "baleia@x.io", "ana@x.io", []byte("x")); err == nil {
		t.Fatal("a silent server was taken for a delivery")
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Errorf("send waited %s on a silent server, want about its 200ms context", waited)
	}
}

// TestSendMail_RefusesCRLF: an address cannot inject SMTP commands.
func TestSendMail_RefusesCRLF(t *testing.T) {
	t.Parallel()
	if err := sendMail(context.Background(), "127.0.0.1:1", "127.0.0.1", nil, "a@x.io", "b@x.io\r\nRCPT TO:<c@x.io>", nil); err == nil {
		t.Error("an address with CRLF was sent")
	}
}
