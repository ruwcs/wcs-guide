package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// ServeHTTP реализует интерфейс http.Handler для handler.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	markdown := strings.HasSuffix(r.URL.Path, ".md")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		h.failure(w, r, 405, markdown)
		return
	}
	value := r.URL.Path
	encoded := strings.ToLower(r.URL.RawPath + r.URL.EscapedPath())
	if !utf8.ValidString(value) || path.Clean(value) != value || strings.Contains(encoded, "%2f") || strings.Contains(encoded, "%5c") {
		h.failure(w, r, 404, markdown)
		return
	}
	switch value {
	case "/healthz":
		h.send(w, r, 200, "text/plain; charset=utf-8", []byte("ok\n"))
		return
	case "/static/site.css":
		css, _ := resources.ReadFile("static/site.css")
		h.send(w, r, 200, "text/css; charset=utf-8", css)
		return
	case "/robots.txt":
		h.send(w, r, 200, "text/plain; charset=utf-8", []byte("User-agent: *\nAllow: /\nSitemap: "+h.absolute("/sitemap.xml")+"\n"))
		return
	case "/sitemap.xml":
		h.sitemap(w, r)
		return
	}
	if markdown {
		value = strings.TrimSuffix(value, ".md")
		if value == "" {
			value = "/"
		}
	}
	if value == "" || domain.ValidatePagePath(value) != nil {
		h.failure(w, r, 404, markdown)
		return
	}
	h.page(w, r, value, markdown)
}

// send отправляет HTTP-ответ.
func (h *handler) send(w http.ResponseWriter, r *http.Request, status int, kind string, body []byte) {
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// failure отправляет публичное сообщение об ошибке.
func (h *handler) failure(w http.ResponseWriter, r *http.Request, status int, markdown bool) {
	message := http.StatusText(status)
	if markdown {
		h.send(w, r, status, "text/plain; charset=utf-8", []byte(message+"\n"))
		return
	}
	data := view{Page: storage.PublicPage{Title: message, Language: "ru"}, Version: h.version, Error: message}
	var body bytes.Buffer
	if err := h.templates.ExecuteTemplate(&body, "layout", data); err != nil {
		h.send(w, r, status, "text/plain; charset=utf-8", []byte(message+"\n"))
		return
	}
	h.send(w, r, status, "text/html; charset=utf-8", body.Bytes())
}

// storageFailure обрабатывает ошибку чтения публичных данных.
func (h *handler) storageFailure(w http.ResponseWriter, r *http.Request, err error, markdown bool) {
	if errors.Is(err, storage.ErrNotFound) {
		h.failure(w, r, 404, markdown)
		return
	}
	h.logger.Error("[web] ошибка формирования ответа", "error", err)
	h.failure(w, r, 500, markdown)
}
