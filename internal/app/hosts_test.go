package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestHostSeparation проверяет разделение хостов и недоверенные forwarded-заголовки.
func TestHostSeparation(t *testing.T) {
	public, _ := url.Parse("http://127.0.0.1:8080")
	editor, _ := url.Parse("http://localhost:8080")
	handler := hostRouter(*public, *editor, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for host, status := range map[string]int{"127.0.0.1:8080": 404, "localhost:8080": 200, "evil.example": 421} {
		request := httptest.NewRequest("GET", "http://"+host+"/login", nil)
		request.Header.Set("X-Forwarded-Host", "localhost:8080")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatal(host, response.Code)
		}
	}
}
