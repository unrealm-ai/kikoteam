package integration

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"html/template"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"strings"
)

//go:embed email/verification.html
var verificationHTML string

// This is the mail-safe PNG rendered from web/public/brand/mark.svg.
//
//go:embed email/kiko-mark.png
var verificationLogo []byte

var verificationTemplate = template.Must(template.New("verification").Parse(verificationHTML))

type smtpMessage struct{ subject, contentType, body string }

func plainMessage(subject, body string) smtpMessage {
	return smtpMessage{subject, "text/plain; charset=UTF-8", body}
}

func verificationMessage(purpose, code string) (smtpMessage, error) {
	action := "验证邮箱"
	switch purpose {
	case "register":
		action = "注册"
	case "login":
		action = "登录"
	case "reset_password":
		action = "重置密码"
	}
	subject := "KikoTeam " + action + "验证码"
	if action == "验证邮箱" {
		subject = "KikoTeam 邮箱验证码"
	}
	var html bytes.Buffer
	if err := verificationTemplate.Execute(&html, struct{ Subject, Action, Code string }{subject, action, code}); err != nil {
		return smtpMessage{}, err
	}
	plain := "KikoTeam · 企业级 AI 员工云平台\r\n\r\n请输入以下验证码完成" + action + "。\r\n\r\n" + code +
		"\r\n\r\n验证码 5 分钟内有效，请勿分享给他人。\r\n如果不是你发起的操作，请忽略这封邮件。\r\n\r\n" +
		"KikoTeam 帮你创建 AI 员工，连接企业工具，让它们在授权范围内接手任务、推进协作并回报结果。"
	body, contentType, err := verificationAlternatives(plain, html.String())
	return smtpMessage{mime.BEncoding.Encode("UTF-8", subject), contentType, body}, err
}

func verificationAlternatives(plain, html string) (string, string, error) {
	var body bytes.Buffer
	alt := multipart.NewWriter(&body)
	if err := quotedMailPart(alt, "text/plain; charset=UTF-8", plain); err != nil {
		return "", "", err
	}
	var relatedBody bytes.Buffer
	related := multipart.NewWriter(&relatedBody)
	if err := quotedMailPart(related, "text/html; charset=UTF-8", html); err != nil {
		return "", "", err
	}
	logo, err := related.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"image/png"}, "Content-Transfer-Encoding": {"base64"},
		"Content-ID": {"<kiko-mark@kiko>"}, "Content-Disposition": {"inline"},
	})
	if err != nil {
		return "", "", err
	}
	if _, err = io.WriteString(logo, wrapMailBase64(base64.StdEncoding.EncodeToString(verificationLogo))); err != nil {
		return "", "", err
	}
	if err = related.Close(); err != nil {
		return "", "", err
	}
	part, err := alt.CreatePart(textproto.MIMEHeader{"Content-Type": {
		mime.FormatMediaType("multipart/related", map[string]string{"boundary": related.Boundary(), "type": "text/html"}),
	}})
	if err != nil {
		return "", "", err
	}
	if _, err = io.Copy(part, &relatedBody); err != nil {
		return "", "", err
	}
	if err = alt.Close(); err != nil {
		return "", "", err
	}
	return body.String(), mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alt.Boundary()}), nil
}

func quotedMailPart(m *multipart.Writer, typ, content string) error {
	part, err := m.CreatePart(textproto.MIMEHeader{"Content-Type": {typ}, "Content-Transfer-Encoding": {"quoted-printable"}})
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(part)
	if _, err = io.WriteString(qp, content); err != nil {
		return err
	}
	return qp.Close()
}

func wrapMailBase64(encoded string) string {
	var wrapped strings.Builder
	for len(encoded) > 76 {
		wrapped.WriteString(encoded[:76])
		wrapped.WriteString("\r\n")
		encoded = encoded[76:]
	}
	wrapped.WriteString(encoded)
	return wrapped.String()
}
