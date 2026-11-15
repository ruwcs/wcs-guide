package domain

import (
	"reflect"
	"testing"
	"time"
)

// TestPublicationBlockedIndependent проверяет независимость запрета публикации от автообновления.
func TestPublicationBlockedIndependent(t *testing.T) {
	for _, auto := range []bool{false, true} {
		for _, blocked := range []bool{false, true} {
			s := Source{Name: "источник", Type: SourceTelegramPost, ExternalRef: "публикация", AutoRefresh: auto, PublicationBlocked: blocked}
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := s.Sync.RecordFailure(instant(), "ошибка"); err != nil {
				t.Fatal(err)
			}
			if err := s.Sync.RecordSuccess(instant().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if s.AutoRefresh != auto || s.PublicationBlocked != blocked {
				t.Fatal("синхронизация изменила признаки")
			}
			s.PublicationBlocked = !blocked
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			if s.AutoRefresh != auto || s.Sync.Status() != SyncSucceeded {
				t.Fatal("блокировка изменила синхронизацию")
			}
		}
	}
	if (Source{}).PublicationBlocked {
		t.Fatal("нулевое значение запрещает публикацию")
	}
}

// TestCanonicalPagePaths проверяет канонические адреса страниц.
func TestCanonicalPagePaths(t *testing.T) {
	for _, value := range []string{"/", "/events/korea", "/школы/Москва", "/한국/행사", "/события-2027", ""} {
		p := page()
		p.Path = value
		if err := p.Validate(); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"/events/../schools", "/events/./korea", "/events//korea", "/%2Fexample.org", "/a%00b", "/events/", "//", "events", "https://example.org", "/a?b", "/a#b", "/a\\b", "/a%b", "/a b", "/a\tb", "/a\nb", "/a\x00b", "/a\x7fb", "/a\u0085b", "/a\u00a0b", string([]byte{'/', 255})} {
		p := page()
		p.Path = value
		before := p
		requireInvalid(t, p.Validate())
		if !reflect.DeepEqual(p, before) {
			t.Fatal("валидация изменила путь")
		}
	}
	p := page()
	p.Path = ""
	p.Status = PagePublished
	p.PublishedAt = p.CreatedAt
	requireInvalid(t, p.Validate())
}
