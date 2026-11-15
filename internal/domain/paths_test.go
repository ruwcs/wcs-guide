package domain

import "testing"

// TestReservedPagePaths проверяет резервирование служебных адресов.
func TestReservedPagePaths(t *testing.T) {
	for _, value := range []string{"/healthz", "/robots.txt", "/sitemap.xml", "/static", "/static/site.css", "/calendar.md", "/.md"} {
		if ValidatePagePath(value) == nil {
			t.Fatal(value)
		}
	}
	for _, value := range []string{"", "/", "/школы/россия", "/calendar", "/static-page"} {
		if err := ValidatePagePath(value); err != nil {
			t.Fatal(value, err)
		}
	}
}
