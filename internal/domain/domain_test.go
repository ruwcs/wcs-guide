package domain

import (
	"bytes"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

// instant возвращает тестовую дату.
func instant() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.FixedZone("test", 10800)) }

// page создаёт тестовую страницу.
func page() Page {
	at := instant()
	return Page{ID: 1, Path: "/календарь", Title: "Календарь", Language: "ru", CreatedAt: at, UpdatedAt: at, Blocks: []Block{{ID: 1, PageID: 1, Type: BlockText, Text: "текст"}}}
}

// imported создаёт тестовый импортируемый блок.
func imported() Block {
	return Block{ID: 2, PageID: 1, Type: BlockImported, Import: &ImportSettings{SourceID: 1}}
}

// requireInvalid проверяет ошибку валидации.
func requireInvalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено %v", err)
	}
}

// TestSyncTransitions проверяет изменения состояния получения источника.
func TestSyncTransitions(t *testing.T) {
	var s SyncState
	at := instant()
	if s.Status() != SyncNever {
		t.Fatal(s)
	}
	if err := s.RecordFailure(at, "временно недоступен"); err != nil {
		t.Fatal(err)
	}
	if s.Status() != SyncFailed || !s.LastSuccessAt.IsZero() {
		t.Fatal(s)
	}
	if err := s.RecordSuccess(at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	success := s.LastSuccessAt
	if s.Status() != SyncSucceeded || s.LastError != "" || success.Location() != time.UTC {
		t.Fatal(s)
	}
	if err := s.RecordFailure(at.Add(2*time.Second), "ошибка"); err != nil {
		t.Fatal(err)
	}
	if !s.LastSuccessAt.Equal(success) {
		t.Fatal("потерян последний успех")
	}
	if err := s.RecordFailure(at, "ошибка"); err != nil {
		t.Fatal(err)
	}
	before := s
	requireInvalid(t, s.RecordFailure(at.Add(3*time.Second), " "))
	requireInvalid(t, s.RecordSuccess(time.Time{}))
	if s != before {
		t.Fatal("ошибка изменила состояние")
	}
	if err := s.RecordSuccess(at.Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if s.Status() != SyncSucceeded {
		t.Fatal(s)
	}
	for _, bad := range []SyncState{{LastError: "ошибка"}, {LastAttemptAt: at}, {LastAttemptAt: at, LastSuccessAt: at.Add(time.Second)}, {LastAttemptAt: at, LastError: " "}} {
		requireInvalid(t, bad.Validate())
	}
}

// TestSourceValidation проверяет проверку свойств источника.
func TestSourceValidation(t *testing.T) {
	valid := Source{Name: "источник", Type: SourceTelegramPost, ExternalRef: "канал/1"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Source){func(s *Source) { s.ID = -1 }, func(s *Source) { s.Name = " " }, func(s *Source) { s.Type = "unknown" }, func(s *Source) { s.ExternalRef = "" }} {
		s := valid
		change(&s)
		requireInvalid(t, s.Validate())
	}
	valid.AutoRefresh = true
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestPageLifecycle проверяет публикацию и снятие страницы.
func TestPageLifecycle(t *testing.T) {
	p := page()
	at := instant().Add(time.Second)
	if p.Status != PageDraft {
		t.Fatal(p.Status)
	}
	before := p
	requireInvalid(t, p.Publish(time.Time{}))
	if !reflect.DeepEqual(p, before) {
		t.Fatal("ошибка изменила страницу")
	}
	if err := p.Publish(at); err != nil {
		t.Fatal(err)
	}
	published := p.PublishedAt
	if err := p.Publish(at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !p.PublishedAt.Equal(published) || !p.UpdatedAt.Equal(at) {
		t.Fatal("повторная публикация изменила время")
	}
	if err := p.Unpublish(at.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if p.Status != PageDraft || !p.PublishedAt.Equal(published) || !reflect.DeepEqual(p.Blocks, before.Blocks) {
		t.Fatal("снятие потеряло данные")
	}
	before = p
	if err := p.Unpublish(at.Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, p) {
		t.Fatal("повторное снятие изменило страницу")
	}
	if err := p.Publish(at.Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if !p.PublishedAt.Equal(at.Add(4 * time.Second)) {
		t.Fatal(p.PublishedAt)
	}
}

// TestPageValidation проверяет проверку страницы и её блоков.
func TestPageValidation(t *testing.T) {
	for _, change := range []func(*Page){func(p *Page) { p.ID = -1 }, func(p *Page) { p.Status = 99 }, func(p *Page) { p.Path = "https://example.org" }, func(p *Page) { p.Path = "//example.org" }, func(p *Page) { p.Path = "/a?b" }, func(p *Page) { p.Path = "/a#b" }, func(p *Page) { p.Blocks[0].PageID = 2 }, func(p *Page) { p.Blocks = append(p.Blocks, p.Blocks[0]) }} {
		p := page()
		change(&p)
		requireInvalid(t, p.Validate())
	}
	p := page()
	p.Title = ""
	p.Language = ""
	p.Path = ""
	if err := p.Validate(); err != nil {
		t.Fatal("неполный черновик", err)
	}
	requireInvalid(t, p.Publish(instant()))
	p = page()
	p.Blocks[0].ID = 0
	p.Blocks = append(p.Blocks, Block{PageID: 1, Type: BlockText})
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Blocks[0].Text != "текст" || p.Blocks[1].Text != "" {
		t.Fatal("потерян порядок")
	}
}

// TestExtractionRulesValidation проверяет проверку текстовых якорей.
func TestExtractionRulesValidation(t *testing.T) {
	for _, r := range []ExtractionRules{{}, {Start: TextAnchor{Text: "📅 Заголовок"}}, {End: TextAnchor{Text: "См также:", Include: true}}, {Start: TextAnchor{Include: true}, End: TextAnchor{Include: true}}} {
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{" ", "\n", "строка\rстрока", string([]byte{255})} {
		requireInvalid(t, (ExtractionRules{Start: TextAnchor{Text: s}}).Validate())
		requireInvalid(t, (ExtractionRules{End: TextAnchor{Text: s}}).Validate())
	}
}

// TestSafeLogging проверяет отсутствие содержимого в техническом журнале.
func TestSafeLogging(t *testing.T) {
	// secret обнаруживает случайный вывод содержимого сущностей в журнал.
	const secret = "CONFIDENTIAL_MARKER"
	b := imported()
	b.Import.Rules.Start.Text = secret
	b.Prepared = &PreparedContent{Content: secret}
	p := page()
	p.MachineContext = secret
	p.Title = secret
	p.Path = secret
	p.Blocks = []Block{b}
	cases := []struct {
		v    any      // Сущность для проверки безопасного логирования.
		keys []string // Ожидаемые атрибуты журнала.
	}{
		{Source{ID: 1, Name: secret, ExternalRef: secret, FetchParams: map[string]string{"token": secret}, Sync: SyncState{LastError: secret}}, []string{"id", "type", "auto_refresh", "publication_blocked", "sync_status"}},
		{p, []string{"id", "status", "block_count"}},
		{b, []string{"id", "page_id", "type", "has_prepared", "source_id"}},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&out, nil))
		logger.Info("entity", "entity", tc.v)
		text := out.String()
		if strings.Contains(text, secret) {
			t.Fatal("утечка в журнал", text)
		}
		for _, key := range tc.keys {
			if !strings.Contains(text, "\""+key+"\":") {
				t.Fatal("нет атрибута", key, text)
			}
		}
	}
}

// TestPageMachineContext проверяет необязательный машинный контекст страницы.
func TestPageMachineContext(t *testing.T) {
	for _, text := range []string{"", "WCS — West Coast Swing.\n한국 <контекст> & \"пример\""} {
		p := page()
		p.MachineContext = text
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
		if err := p.Publish(instant()); err != nil {
			t.Fatal(err)
		}
	}
	p := page()
	p.MachineContext = string([]byte{255})
	requireInvalid(t, p.Validate())
	p = page()
	p.MachineContext = "контекст"
	p.Title = ""
	requireInvalid(t, p.Publish(instant()))
}
