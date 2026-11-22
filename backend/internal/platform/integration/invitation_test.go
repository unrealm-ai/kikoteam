package integration

import (
	"bufio"
	"io"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

func TestSMTPInvitationOutcomes(t *testing.T) {
	for _, tc := range []struct{ name, reply, want string }{
		{"accepted-quit-disconnect", "250 queued\r\n", "sent"},
		{"explicit-reject", "550 rejected\r\n", "failed"},
		{"disconnect-after-data", "", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer func() { _ = clientConn.Close() }()
			if err := clientConn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			headers := make(chan string, 1)
			go func() {
				defer close(done)
				defer func() { _ = serverConn.Close() }()
				if _, err := io.WriteString(serverConn, "220 localhost SMTP\r\n"); err != nil {
					return
				}
				reader := bufio.NewReader(serverConn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if strings.HasPrefix(line, "EHLO") || strings.HasPrefix(line, "HELO") {
						if _, err := io.WriteString(serverConn, "250 localhost\r\n"); err != nil {
							return
						}
						continue
					}
					if strings.HasPrefix(line, "DATA") {
						if _, err := io.WriteString(serverConn, "354 proceed\r\n"); err != nil {
							return
						}
						var captured strings.Builder
						for {
							line, err = reader.ReadString('\n')
							if err != nil {
								return
							}
							captured.WriteString(line)
							if line == ".\r\n" {
								break
							}
						}
						headers <- captured.String()
						if tc.reply != "" {
							if _, err := io.WriteString(serverConn, tc.reply); err != nil {
								return
							}
						}
						return
					}
				}
			}()
			client, err := smtp.NewClient(clientConn, "localhost")
			if err != nil {
				t.Fatal(err)
			}
			status, err := submitMessage(client, "from@example.test", "to@example.test", "Invitation", "test body", "stable-id@example.test")
			if status != tc.want || (err == nil) != (tc.want == "sent") {
				t.Fatalf("status=%s err=%v", status, err)
			}
			<-done
			if !strings.Contains(<-headers, "Message-ID: <stable-id@example.test>\r\n") {
				t.Fatal("invalid Message-ID header")
			}
		})
	}
}
