package web

import (
	"encoding/xml"
	"net/http"
	"time"
)

// sitemapURL — адрес и дата обновления материала в карте сайта.
type sitemapURL struct {
	Loc     string `xml:"loc"`               // Канонический адрес материала.
	Lastmod string `xml:"lastmod,omitempty"` // Известная дата публичного изменения.
}

// sitemapDocument — карта опубликованных страниц.
type sitemapDocument struct {
	XMLName   xml.Name     `xml:"urlset"`     // Корневой элемент карты сайта.
	Namespace string       `xml:"xmlns,attr"` // Пространство имён sitemap.
	URLs      []sitemapURL `xml:"url"`        // Публичные HTML-страницы.
}

// sitemap отправляет карту опубликованных страниц.
func (h *handler) sitemap(w http.ResponseWriter, r *http.Request) {
	pages, err := h.store.ListPublicPages(r.Context())
	if err != nil {
		h.storageFailure(w, r, err, true)
		return
	}
	document := sitemapDocument{Namespace: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: []sitemapURL{{Loc: h.absolute("/")}}}
	for _, page := range pages {
		if page.Path == "/" {
			continue
		}
		entry := sitemapURL{Loc: h.absolute(page.Path)}
		if !page.UpdatedAt.IsZero() {
			entry.Lastmod = page.UpdatedAt.UTC().Format(time.RFC3339Nano)
		}
		document.URLs = append(document.URLs, entry)
	}
	output, err := xml.Marshal(document)
	if err != nil {
		h.storageFailure(w, r, err, true)
		return
	}
	h.send(w, r, 200, "application/xml; charset=utf-8", append([]byte(xml.Header), output...))
}
