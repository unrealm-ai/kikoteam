package integration

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestSMTPRejectsPlaintextServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ln, e := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = ln.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer func() { _ = c.Close() }()
		if _, e = fmt.Fprint(c, "220 test SMTP\r\n"); e != nil {
			return
		}
		if _, e = bufio.NewReader(c).ReadString('\n'); e != nil {
			return
		}
		if _, e = fmt.Fprint(c, "250 test SMTP\r\n"); e != nil {
			return
		}
	}()
	s := SMTP{Address: ln.Addr().String(), Host: "localhost", From: "test@example.test"}
	e = s.SendCode(ctx, "user@example.test", "register", "123456")
	if e == nil || !strings.Contains(e.Error(), "requires TLS") {
		t.Fatalf("plaintext server accepted: %v", e)
	}
	<-done
}

func TestSMTPSettingsProbeAndTestMailWithTLS(t *testing.T) {
	// Reuse a test certificate and trust only that cert in this fixture.
	httpTLS := httptest.NewTLSServer(nil)
	cert := httpTLS.Certificate()
	keyPair := httpTLS.TLS.Certificates[0]
	httpTLS.Close()
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{keyPair}, MinVersion: tls.VersionTLS12})
	t.Cleanup(func() { _ = tlsListener.Close() })
	results := make(chan error, 2)
	for _, sending := range []bool{false, true} {
		go func(send bool) {
			conn, err := tlsListener.Accept()
			if err != nil {
				results <- err
				return
			}
			defer func() { _ = conn.Close() }()
			if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				results <- err
				return
			}
			results <- serveSMTPSettingsFixture(conn, send, true, "fixture")
		}(sending)
		s := SMTP{Address: listener.Addr().String(), Host: cert.DNSNames[0], From: "from@example.test",
			ImplicitTLS: true, RootCAs: roots, Username: "fixture", Password: "mock-password"}
		if sending {
			err = s.SendTest(ctx, "admin@example.test")
		} else {
			err = s.Probe(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

// A standard self-host can require STARTTLS, then authenticate using the
// same address as the message sender. No alias or cleartext fallback is needed.
func TestSMTPTestMailWithSTARTTLSSelfHosted(t *testing.T) {
	https := httptest.NewTLSServer(nil)
	cert, key := https.Certificate(), https.TLS.Certificates[0]
	https.Close()
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	const sender = "from@example.test"
	done := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer func() { _ = c.Close() }()
		if e = c.SetDeadline(time.Now().Add(3 * time.Second)); e != nil {
			done <- e
			return
		}
		for _, step := range [][2]string{
			{"", "220 test SMTP\r\n"},
			{"EHLO ", "250-test SMTP\r\n250 STARTTLS\r\n"},
			{"STARTTLS", "220 ready for TLS\r\n"},
		} {
			if step[0] != "" {
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil || !strings.HasPrefix(line, step[0]) {
					done <- fmt.Errorf("expected SMTP command %q before TLS", step[0])
					return
				}
			}
			if _, e = fmt.Fprint(c, step[1]); e != nil {
				done <- e
				return
			}
		}
		secure := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{key}, MinVersion: tls.VersionTLS12})
		done <- serveSMTPSettingsFixture(secure, true, false, sender)
	}()
	server := &SMTP{Address: listener.Addr().String(), Host: cert.DNSNames[0], Username: sender, Password: "mock-password", From: sender, RootCAs: roots}
	if err = server.SendTest(ctx, "admin@example.test"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func serveSMTPSettingsFixture(conn net.Conn, send, greeting bool, account string) error {
	r := bufio.NewReader(conn)
	exchanges := [][2]string{
		{"", "220 test SMTP\r\n"},
		{"EHLO ", "250-test SMTP\r\n250 AUTH PLAIN\r\n"},
		{"AUTH PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00"+account+"\x00mock-password")), "235 test auth accepted\r\n"},
	}
	if !greeting {
		exchanges = exchanges[1:]
	}
	if send {
		exchanges = append(exchanges, [2]string{"MAIL FROM:<from@example.test>", "250 ok\r\n"},
			[2]string{"RCPT TO:<admin@example.test>", "250 ok\r\n"}, [2]string{"DATA", "354 continue\r\n"})
	}
	for _, pair := range exchanges {
		if pair[0] != "" {
			line, err := r.ReadString('\n')
			if err != nil || !strings.HasPrefix(line, pair[0]) {
				return fmt.Errorf("unexpected SMTP fixture command")
			}
		}
		if _, err := fmt.Fprint(conn, pair[1]); err != nil {
			return err
		}
	}
	if send {
		subjectSeen := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return err
			}
			if line == ".\r\n" {
				break
			}
			if line == "Subject: KIKO SMTP test\r\n" {
				subjectSeen = true
			}
		}
		if !subjectSeen {
			return fmt.Errorf("SMTP fixture missing test subject")
		}
		if _, err := fmt.Fprint(conn, "250 accepted\r\n"); err != nil {
			return err
		}
	}
	line, err := r.ReadString('\n')
	if err != nil || line != "QUIT\r\n" {
		return fmt.Errorf("expected SMTP QUIT")
	}
	_, err = fmt.Fprint(conn, "221 done\r\n")
	return err
}

func TestRuntimeSMTPDisabledDoesNotDial(t *testing.T) {
	smtp := RuntimeSMTP{Resolve: func(context.Context) (service.MailSettingsDraft, error) {
		return service.MailSettingsDraft{MailSettings: service.MailSettings{Address: "127.0.0.1:1"}}, nil
	}}
	if err := smtp.SendCode(t.Context(), "admin@example.test", "login", "123456"); err == nil || err.Error() != "email service disabled" {
		t.Fatal("disabled settings used SMTP", err)
	}
}
