// Package editor предоставляет защищённый редакторский интерфейс.
package editor

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ruwcs/wcs-guide/internal/auth"
	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// resources — шаблоны и ресурсы редакторского интерфейса.
//
//go:embed templates/*.html static/*
var resources embed.FS

// handler — редакторский веб-интерфейс.
type handler struct {
	auth      *auth.Service      // Аутентификация пользователей.
	templates *template.Template // HTML-шаблоны редактора.
	origin    string             // Допустимый Origin запросов.
	secure    bool               // Защищённая передача cookie.
	logger    *slog.Logger       // Журнал приложения.
}

// page — данные страницы редактора.
type page struct {
	Kind string      // Вид страницы.
	User domain.User // Текущий пользователь.
}

// New возвращает обработчик редакторского интерфейса.
func New(service *auth.Service, origin url.URL, logger *slog.Logger) (http.Handler, error) {
	templates, err := template.ParseFS(resources, "templates/*.html")
	if err != nil {
		return nil, err
	}
	h := &handler{auth: service, templates: templates, origin: origin.String(), secure: origin.Scheme == "https", logger: logger}
	return http.NewCrossOriginProtection().Handler(h), nil
}

// cookieName возвращает имя cookie редактора.
func (h *handler) cookieName(kind string) string {
	prefix := "wcs-editor-"
	if h.secure {
		prefix = "__Host-" + prefix
	}
	return prefix + kind
}

// cookieValue читает cookie браузера.
func (h *handler) cookieValue(r *http.Request, kind string) string {
	cookie, err := r.Cookie(h.cookieName(kind))
	if err != nil {
		return ""
	}
	return cookie.Value
}

// setCookie устанавливает cookie редактора.
func (h *handler) setCookie(w http.ResponseWriter, kind, value string, lifetime time.Duration) {
	maxAge := int(lifetime.Seconds())
	expires := time.Now().Add(lifetime)
	if value == "" {
		maxAge = -1
		expires = time.Unix(1, 0)
	}
	http.SetCookie(w, &http.Cookie{Name: h.cookieName(kind), Value: value, Path: "/", HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge, Expires: expires})
}

// ServeHTTP реализует интерфейс http.Handler для handler.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if r.Method == "GET" || r.Method == "HEAD" {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if r.Method == "GET" {
				w.Write([]byte("User-agent: *\nDisallow: /\n"))
			}
			return
		case "/static/editor.js", "/static/editor.css":
			name := strings.TrimPrefix(r.URL.Path, "/")
			data, err := resources.ReadFile(name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(name, ".js") {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			} else {
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
			}
			if r.Method == "GET" {
				w.Write(data)
			}
			return
		case "/login", "/register":
			h.render(w, r, page{Kind: strings.TrimPrefix(r.URL.Path, "/")})
			return
		case "/":
			u, err := h.auth.SessionUser(r.Context(), h.cookieValue(r, "session"))
			if err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					h.setCookie(w, "session", "", 0)
					http.Redirect(w, r, "/login", 303)
				} else {
					h.fail(w, err)
				}
				return
			}
			h.render(w, r, page{Kind: "home", User: u})
			return
		default:
			http.NotFound(w, r)
			return
		}
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "Метод не поддерживается", 405)
		return
	}
	if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != h.origin {
		http.Error(w, "Недопустимый источник запроса", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if r.URL.Path == "/logout" {
		if _, err := h.auth.SessionUser(r.Context(), h.cookieValue(r, "session")); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				http.Error(w, "Требуется вход", 401)
			} else {
				h.fail(w, err)
			}
			return
		}
		if err := h.auth.Logout(r.Context(), h.cookieValue(r, "session")); err != nil {
			h.fail(w, err)
			return
		}
		h.setCookie(w, "session", "", 0)
		http.Redirect(w, r, "/login", 303)
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		http.Error(w, "Требуется JSON", 415)
		return
	}
	paths := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(paths) != 3 || paths[0] != "auth" || (paths[1] != "login" && paths[1] != "register") {
		http.NotFound(w, r)
		return
	}
	kind, step := paths[1], paths[2]
	switch step {
	case "begin":
		var input struct {
			Value string // Логин или регистрационный код.
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "Некорректный запрос", 400)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "Некорректный запрос", 400)
			return
		}
		id, options, err := h.auth.Begin(r.Context(), kind, input.Value, h.cookieValue(r, "operation"))
		if err != nil {
			h.fail(w, err)
			return
		}
		h.setCookie(w, "operation", id, 5*time.Minute)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(options)
	case "finish":
		token, err := h.auth.Finish(r.Context(), kind, h.cookieValue(r, "operation"), r)
		h.setCookie(w, "operation", "", 0)
		if err != nil {
			h.fail(w, err)
			return
		}
		if kind == "login" {
			h.setCookie(w, "session", token, auth.SessionLifetime)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			OK bool // Успех операции.
		}{true})
	default:
		http.NotFound(w, r)
	}
}

// maxRequestBytes — максимальный размер запроса WebAuthn в байтах.
const maxRequestBytes = 64 << 10

// render отправляет страницу редактора.
func (h *handler) render(w http.ResponseWriter, r *http.Request, data page) {
	var body bytes.Buffer
	if err := h.templates.ExecuteTemplate(&body, "page", data); err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != "HEAD" {
		w.Write(body.Bytes())
	}
}

// fail отправляет сообщение об ошибке аутентификации.
func (h *handler) fail(w http.ResponseWriter, err error) {
	status := 500
	if errors.Is(err, auth.ErrDenied) || errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrUnique) {
		status = 400
	} else if errors.Is(err, auth.ErrLimited) {
		status = 429
	} else {
		h.logger.Error("[auth] внутренняя ошибка")
	}
	http.Error(w, "Операция не выполнена. Проверьте данные и начните заново.", status)
}
