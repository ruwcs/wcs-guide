package domain

import (
	"reflect"
	"testing"
	"time"
)

func prepared() PreparedContent {
	return PreparedContent{SourceID: 1, SnapshotID: 1, PreparedAt: instant(), Content: "успешный результат"}
}

func TestPreparedSourceAndSettings(t *testing.T) {
	b := imported()
	r := prepared()
	if err := b.ApplyPrepared(r); err != nil {
		t.Fatal(err)
	}
	b.Import.Rules.Start.Text = "новые правила"
	if err := b.Validate(); err != nil {
		t.Fatal("старый результат допустим", err)
	}
	old := b.Prepared
	for _, change := range []func(*PreparedContent){
		func(p *PreparedContent) { p.SourceID = 2; p.Rules = b.Import.Rules },
		func(p *PreparedContent) { p.SourceID = 0 },
		func(p *PreparedContent) { p.SourceID = -1 },
		func(p *PreparedContent) { p.Rules = ExtractionRules{} },
		func(p *PreparedContent) { p.Rules = b.Import.Rules; p.Rules.Start.Include = true },
	} {
		result := r
		change(&result)
		before := b
		settingsBefore := *b.Import
		resultBefore := *b.Prepared
		requireInvalid(t, b.ApplyPrepared(result))
		if !reflect.DeepEqual(b, before) || *b.Import != settingsBefore || *b.Prepared != resultBefore || b.Prepared != old {
			t.Fatal("неудача изменила блок")
		}
	}
	next := r
	next.Rules = b.Import.Rules
	if err := b.ApplyPrepared(next); err != nil {
		t.Fatal(err)
	}
	next.Content = "изменение аргумента"
	if b.Prepared.Content != r.Content {
		t.Fatal("результат не скопирован")
	}
	b.Import.SourceID = 2
	requireInvalid(t, b.Validate())
}

func TestReplaceImport(t *testing.T) {
	b := imported()
	r := prepared()
	if err := b.ApplyPrepared(r); err != nil {
		t.Fatal(err)
	}
	settings := ImportSettings{SourceID: 2, Rules: ExtractionRules{End: TextAnchor{Text: "конец"}}}
	result := prepared()
	result.SourceID = 2
	result.SnapshotID = 2
	result.Rules = settings.Rules
	for _, change := range []func(*ImportSettings, *PreparedContent){
		func(s *ImportSettings, p *PreparedContent) { s.SourceID = 0 },
		func(s *ImportSettings, p *PreparedContent) { p.SourceID = 1 },
		func(s *ImportSettings, p *PreparedContent) { p.Rules = ExtractionRules{} },
		func(s *ImportSettings, p *PreparedContent) { p.PreparedAt = time.Time{} },
	} {
		s, p := settings, result
		change(&s, &p)
		before := b
		oldSettings := *b.Import
		oldResult := *b.Prepared
		requireInvalid(t, b.ReplaceImport(s, p))
		if !reflect.DeepEqual(b, before) || *b.Import != oldSettings || *b.Prepared != oldResult {
			t.Fatal("замена частично применилась")
		}
	}
	if err := b.ReplaceImport(settings, result); err != nil {
		t.Fatal(err)
	}
	if *b.Import != settings || *b.Prepared != result {
		t.Fatal("замена не применена")
	}
	settings.SourceID = 9
	result.Content = "изменение аргумента"
	if b.Import.SourceID != 2 || b.Prepared.Content == result.Content {
		t.Fatal("аргументы не скопированы")
	}
	stale := r
	stale.Rules = b.Import.Rules
	requireInvalid(t, b.ApplyPrepared(stale))
	manual := Block{Type: BlockText}
	requireInvalid(t, manual.ReplaceImport(settings, result))
	invalidBlock := b
	invalidBlock.ID = -1
	requireInvalid(t, invalidBlock.ReplaceImport(settings, result))
}

func TestPublicationBlockedIndependent(t *testing.T) {
	for _, auto := range []bool{false, true} {
		for _, blocked := range []bool{false, true} {
			s := Source{Name: "источник", Type: SourceTelegramPost, ExternalRef: "публикация", UpdateInterval: time.Hour, AutoRefresh: auto, PublicationBlocked: blocked}
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
