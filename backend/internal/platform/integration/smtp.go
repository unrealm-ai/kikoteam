// Package integration contains outbound platform service clients.
package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type SMTP struct {
	Address, Host, Username, Password, From string
	ImplicitTLS                             bool
	RootCAs                                 *x509.CertPool
}

func (s *SMTP) SendCode(ctx context.Context, to, purpose, code string) error {
	_, err := s.send(ctx, to, "Kiko verification code", "您的 Kiko 验证码是 "+code+"，5 分钟内有效。如非本人操作，请忽略。", "")
	return err
}
func (s *SMTP) SendInvitation(ctx context.Context, to, link, messageID string) (string, error) {
	return s.send(ctx, to, "Kiko workspace invitation", "您收到 Kiko 工作区邀请。请登录后访问以下链接：\r\n"+link+"\r\n如非预期，请忽略此邮件。", messageID)
}
func (s *SMTP) SendTest(ctx context.Context, to string) error {
	_, err := s.send(ctx, to, "KIKO SMTP test", "KIKO 企业级AI员工云平台 SMTP 测试邮件。", "")
	return err
}

func (s *SMTP) Probe(ctx context.Context) error {
	return s.withClient(ctx, func(client *smtp.Client) error {
		return client.Quit()
	})
}

func (s *SMTP) send(ctx context.Context, to, subject, body, messageID string) (string, error) {
	if s.Address == "" || strings.ContainsAny(s.From+to+messageID, "\r\n") {
		return "failed", fmt.Errorf("SMTP not configured or invalid envelope")
	}
	status := "failed"
	err := s.withClient(ctx, func(client *smtp.Client) error {
		if e := client.Mail(s.From); e != nil {
			return e
		}
		if e := client.Rcpt(to); e != nil {
			return e
		}
		var e error
		status, e = submitMessage(client, s.From, to, subject, body, messageID)
		return e
	})
	return status, err
}

func (s *SMTP) withClient(ctx context.Context, use func(*smtp.Client) error) error {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, e := dialer.DialContext(ctx, "tcp", s.Address)
	if e != nil {
		return fmt.Errorf("SMTP connect: %w", e)
	}
	defer conn.Close() //nolint:errcheck // Best-effort socket cleanup after protocol completion or original error.
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if e = conn.SetDeadline(deadline); e != nil {
		return e
	}
	rawConn := conn
	stop := context.AfterFunc(ctx, func() {
		_ = rawConn.Close() //nolint:errcheck // Cancellation deliberately interrupts the socket; main operation reports failure.
	})
	defer stop()
	config := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12, RootCAs: s.RootCAs}
	if s.ImplicitTLS {
		secure := tls.Client(conn, config)
		if e = secure.HandshakeContext(ctx); e != nil {
			return e
		}
		conn = secure
	}
	client, e := smtp.NewClient(conn, s.Host)
	if e != nil {
		return e
	}
	defer client.Close() //nolint:errcheck // Quit or the original protocol error determines the send outcome.
	if !s.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP requires TLS")
		}
		if e = client.StartTLS(config); e != nil {
			return e
		}
	}
	if s.Username != "" {
		if e = client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); e != nil {
			return e
		}
	}
	return use(client)
}

func SMTPForSettings(d service.MailSettingsDraft) *SMTP {
	return &SMTP{Address: d.Address, Host: d.Host, Username: d.Username, Password: d.Password, From: d.From, ImplicitTLS: d.ImplicitTLS}
}

type RuntimeSMTP struct {
	Resolve func(context.Context) (service.MailSettingsDraft, error)
}

func (s RuntimeSMTP) SendCode(ctx context.Context, to, purpose, code string) error {
	current, err := s.Resolve(ctx)
	if err != nil {
		return err
	}
	if !current.Enabled {
		return errors.New("email service disabled")
	}
	return SMTPForSettings(current).SendCode(ctx, to, purpose, code)
}

func (s RuntimeSMTP) SendInvitation(ctx context.Context, to, link, messageID string) (string, error) {
	current, err := s.Resolve(ctx)
	if err != nil {
		return "failed", err
	}
	if !current.Enabled {
		return "failed", errors.New("email service disabled")
	}
	return SMTPForSettings(current).SendInvitation(ctx, to, link, messageID)
}

func submitMessage(client *smtp.Client, from, to, subject, body, messageID string) (string, error) {
	w, err := client.Data()
	if err != nil {
		return "failed", err
	}
	// Once DATA body transmission starts, a disconnect has an uncertain outcome.
	headers := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n"
	if messageID != "" {
		headers += "Message-ID: <" + messageID + ">\r\n"
	}
	if _, err = fmt.Fprint(w, headers+"\r\n"+body+"\r\n"); err != nil {
		return "unknown", err
	}
	if err = w.Close(); err != nil {
		var rejection *textproto.Error
		if errors.As(err, &rejection) {
			return "failed", err
		}
		return "unknown", err
	}
	// SMTP acceptance is authoritative; QUIT failure cannot turn it into failure.
	_ = client.Quit() //nolint:errcheck // Message accepted already; disconnect does not revoke SMTP acceptance.
	return "sent", nil
}
