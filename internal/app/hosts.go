package app

import (
	"net/http"
	"net/url"
	"strings"
)

// authority возвращает каноническое имя хоста с портом.
func authority(host, scheme string) string {
	host = strings.ToLower(host)
	if strings.HasSuffix(host, ":443") && scheme == "https" {
		host = strings.TrimSuffix(host, ":443")
	}
	if strings.HasSuffix(host, ":80") && scheme == "http" {
		host = strings.TrimSuffix(host, ":80")
	}
	return host
}

// hostRouter разделяет публичные и редакторские запросы.
func hostRouter(public, editor url.URL, publicHandler, editorHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if strings.ContainsAny(host, "/\\@ ") {
			http.Error(w, "Недопустимый хост", 421)
			return
		}
		switch {
		case authority(host, public.Scheme) == authority(public.Host, public.Scheme):
			publicHandler.ServeHTTP(w, r)
		case authority(host, editor.Scheme) == authority(editor.Host, editor.Scheme):
			editorHandler.ServeHTTP(w, r)
		default:
			http.Error(w, "Неизвестный хост", 421)
		}
	})
}
