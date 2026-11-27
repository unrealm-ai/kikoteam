package integration

import (
	"encoding/base64"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

func TestVerificationEmailSubjects(t *testing.T) {
	for purpose, want := range map[string]string{
		"login": "KikoTeam 登录验证码", "register": "KikoTeam 注册验证码", "reset_password": "KikoTeam 重置密码验证码",
	} {
		t.Run(purpose, func(t *testing.T) {
			msg, err := verificationMessage(purpose, "042681")
			if err != nil {
				t.Fatal(err)
			}
			subject, err := new(mime.WordDecoder).DecodeHeader(msg.subject)
			if err != nil || subject != want || strings.Contains(subject, "042681") {
				t.Fatalf("unexpected subject %q (%v)", subject, err)
			}
		})
	}
}

func TestVerificationEmailIncludesStandaloneCodeAndInlineLogo(t *testing.T) {
	msg, err := verificationMessage("login", "042681")
	if err != nil {
		t.Fatal(err)
	}
	alternative := readVerificationMultipart(t, msg.contentType, "multipart/alternative", strings.NewReader(msg.body))
	plain := nextVerificationPart(t, alternative)
	text := readVerificationPart(t, plain)
	if plain.Header.Get("Content-Type") != "text/plain; charset=UTF-8" ||
		!strings.Contains(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n042681\n\n") ||
		strings.Count(text, "042681") != 1 || !strings.Contains(text, "5 分钟内有效") ||
		!strings.Contains(text, "企业级 AI 员工云平台") {
		t.Fatalf("unexpected plain-text message: %q", text)
	}

	part := nextVerificationPart(t, alternative)
	related := readVerificationMultipart(t, part.Header.Get("Content-Type"), "multipart/related", part)
	html := nextVerificationPart(t, related)
	body := readVerificationPart(t, html)
	if html.Header.Get("Content-Type") != "text/html; charset=UTF-8" ||
		!strings.Contains(body, ">042681</span>") || strings.Count(body, "042681") != 1 ||
		!strings.Contains(body, `src="cid:kiko-mark@kiko"`) || !strings.Contains(body, "推进协作并回报结果") {
		t.Fatal("HTML is missing standalone code, logo, or introduction")
	}

	logo := nextVerificationPart(t, related)
	if logo.Header.Get("Content-ID") != "<kiko-mark@kiko>" ||
		logo.Header.Get("Content-Type") != "image/png" || logo.Header.Get("Content-Disposition") != "inline" ||
		logo.Header.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatalf("unexpected logo headers: %v", logo.Header)
	}
	img, err := png.Decode(base64.NewDecoder(base64.StdEncoding, logo))
	if err != nil || img.Bounds().Dx() != 104 || img.Bounds().Dy() != 86 {
		t.Fatalf("unexpected logo image: %v", err)
	}
	if _, err := related.NextPart(); err != io.EOF {
		t.Fatalf("unexpected trailing related part: %v", err)
	}
	if _, err := alternative.NextPart(); err != io.EOF {
		t.Fatalf("unexpected trailing alternative part: %v", err)
	}
}

func readVerificationMultipart(t *testing.T, contentType, want string, body io.Reader) *multipart.Reader {
	t.Helper()
	typ, params, err := mime.ParseMediaType(contentType)
	if err != nil || typ != want || params["boundary"] == "" {
		t.Fatalf("unexpected MIME type %q: %v", contentType, err)
	}
	return multipart.NewReader(body, params["boundary"])
}

func nextVerificationPart(t *testing.T, reader *multipart.Reader) *multipart.Part {
	t.Helper()
	part, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	return part
}

func readVerificationPart(t *testing.T, part *multipart.Part) string {
	t.Helper()
	out, err := io.ReadAll(part)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
