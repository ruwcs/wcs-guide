package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/ruwcs/wcs-guide/internal/storage"
)

// page отправляет публичное представление страницы.
func (h *handler) page(w http.ResponseWriter, r *http.Request, value string, markdown bool) {
	page, err := h.store.GetPublicPage(r.Context(), value)
	if value == "/" && errors.Is(err, storage.ErrNotFound) {
		page = storage.PublicPage{Path: "/", Title: "WCS Guide", Language: "ru"}
		err = nil
	}
	if err != nil {
		h.storageFailure(w, r, err, markdown)
		return
	}
	data := view{Page: page, Canonical: h.absolute(value), Markdown: h.absolute(strings.TrimSuffix(value, "/") + ".md"), Version: h.version}
	if value == "/" {
		data.Catalog, err = h.store.ListPublicPages(r.Context())
		if err != nil {
			h.storageFailure(w, r, err, markdown)
			return
		}
		filtered := data.Catalog[:0]
		for _, entry := range data.Catalog {
			if entry.Path != "/" {
				filtered = append(filtered, entry)
			}
		}
		data.Catalog = filtered
	}
	var body strings.Builder
	body.WriteString("# " + escape(page.Title) + "\n\n")
	if page.Description != "" {
		body.WriteString(escape(page.Description) + "\n\n")
	}
	if page.MachineContext != "" {
		body.WriteString("## Дополнительный контекст\n\n" + escape(page.MachineContext) + "\n\n")
	}
	seen := make(map[int64]bool)
	size := 0
	for _, block := range page.Blocks {
		size += len(block.Content)
		if size > maxPageBytes {
			h.storageFailure(w, r, fmt.Errorf("web: превышен размер страницы"), markdown)
			return
		}
		html, normalized, err := renderMarkdown(block.Content)
		if err != nil {
			h.storageFailure(w, r, err, markdown)
			return
		}
		data.Blocks = append(data.Blocks, template.HTML(html))
		body.WriteString("---\n\n" + normalized + "\n")
		if block.SourceID != 0 && !seen[block.SourceID] {
			seen[block.SourceID] = true
			if !safeURL(block.ExternalRef) {
				block.SourceName += " (" + block.ExternalRef + ")"
				block.ExternalRef = ""
			}
			data.Sources = append(data.Sources, block)
		}
	}
	if len(data.Catalog) > 0 {
		body.WriteString("---\n\n## Материалы\n\n")
		for _, entry := range data.Catalog {
			body.WriteString("- [" + escape(entry.Title) + "](<" + h.absolute(entry.Path) + ">)")
			if entry.Description != "" {
				body.WriteString(" — " + escape(strings.ReplaceAll(entry.Description, "\n", " ")))
			}
			body.WriteString("\n")
		}
		body.WriteString("\n")
	}
	if len(data.Sources) > 0 {
		body.WriteString("---\n\n## Источники\n\n")
		for _, entry := range data.Sources {
			label := escape(strings.ReplaceAll(entry.SourceName, "\n", " "))
			if entry.ExternalRef != "" {
				body.WriteString("- [" + label + "](<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(entry.ExternalRef) + ">)\n")
			} else {
				body.WriteString("- " + label + "\n")
			}
		}
		body.WriteString("\n")
	}
	if !page.UpdatedAt.IsZero() {
		body.WriteString("Актуализировано: " + page.UpdatedAt.UTC().Format(time.RFC3339Nano) + "\n\n")
	}
	body.WriteString("Канонический адрес: " + data.Canonical + "\n")
	if markdown {
		h.send(w, r, 200, "text/markdown; charset=utf-8", []byte(body.String()))
		return
	}
	w.Header().Set("Link", "<"+data.Markdown+">; rel=\"alternate\"; type=\"text/markdown\"")
	var output bytes.Buffer
	if err := h.templates.ExecuteTemplate(&output, "layout", data); err != nil {
		h.storageFailure(w, r, err, false)
		return
	}
	h.send(w, r, 200, "text/html; charset=utf-8", output.Bytes())
}
