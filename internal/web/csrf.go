package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const csrfCookie = "animarr_csrf"

var errCSRF = errors.New("request blocked; reload the settings page and try again")

func (s *Server) csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && validToken(c.Value) {
		return c.Value
	}
	tok := newToken()
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	})
	return tok
}

func (s *Server) checkCSRF(r *http.Request) error {
	if err := sameOrigin(r); err != nil {
		return err
	}
	c, err := r.Cookie(csrfCookie)
	if err != nil || !validToken(c.Value) {
		return errCSRF
	}
	got := r.FormValue("csrf")
	if len(got) != len(c.Value) || subtle.ConstantTimeCompare([]byte(got), []byte(c.Value)) != 1 {
		return errCSRF
	}
	return nil
}

func sameOrigin(r *http.Request) error {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errCSRF
	}
	if hostMatch(u.Host, r.Host) {
		return nil
	}
	if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); fwd != "" && hostMatch(u.Host, fwd) {
		return nil
	}
	return errCSRF
}

func hostMatch(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(strings.Split(a, ",")[0]))
	b = strings.ToLower(strings.TrimSpace(strings.Split(b, ",")[0]))
	if a == b {
		return true
	}
	ah, ap, ea := net.SplitHostPort(a)
	bh, bp, eb := net.SplitHostPort(b)
	if ea != nil {
		ah, ap = a, ""
	}
	if eb != nil {
		bh, bp = b, ""
	}
	if !strings.EqualFold(ah, bh) {
		return false
	}
	return ap == bp || ap == "" && (bp == "80" || bp == "443") || bp == "" && (ap == "80" || ap == "443")
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func validToken(s string) bool {
	if len(s) < 20 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
