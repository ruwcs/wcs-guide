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

func instant() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.FixedZone("test", 10800)) }
func page() Page {
	at := instant()
	return Page{ID: 1, Path: "/календарь", Title: "Календарь", Language: "ru", CreatedAt: at, UpdatedAt: at, Blocks: []Block{{ID: 1, PageID: 1, Type: BlockText, Text: "текст"}}}
}
func imported() Block {
	return Block{ID: 2, PageID: 1, Type: BlockImported, Import: &ImportSettings{SourceID: 1}}
}
func requireInvalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено %v", err)
	}
}

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
	before := s
	requireInvalid(t, s.RecordFailure(at, "ошибка"))
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

func TestSourceValidation(t *testing.T) {
	valid := Source{Name: "источник", Type: SourceTelegramPost, ExternalRef: "канал/1"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Source){func(s *Source) { s.ID = -1 }, func(s *Source) { s.Name = " " }, func(s *Source) { s.Type = "unknown" }, func(s *Source) { s.ExternalRef = "" }, func(s *Source) { s.AutoRefresh = true }, func(s *Source) { s.UpdateInterval = -1 }} {
		s := valid
		change(&s)
		requireInvalid(t, s.Validate())
	}
	valid.AutoRefresh = true
	valid.UpdateInterval = time.Hour
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotValidation(t *testing.T) {
	valid := Snapshot{SourceID: 1, RawContent: []byte("оригинал"), Format: "test", Checksum: "digest", FetchedAt: instant(), SourceModifiedAt: instant().Add(time.Hour)}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Snapshot){func(s *Snapshot) { s.ID = -1 }, func(s *Snapshot) { s.SourceID = 0 }, func(s *Snapshot) { s.RawContent = nil }, func(s *Snapshot) { s.Format = "" }, func(s *Snapshot) { s.Checksum = "" }, func(s *Snapshot) { s.FetchedAt = time.Time{} }} {
		s := valid
		change(&s)
		requireInvalid(t, s.Validate())
	}
}

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

func TestPageValidation(t *testing.T) {
	for _, change := range []func(*Page){func(p *Page) { p.ID = -1 }, func(p *Page) { p.Status = 99 }, func(p *Page) { p.Path = "https://example.org" }, func(p *Page) { p.Path = "//example.org" }, func(p *Page) { p.Path = "/a?b" }, func(p *Page) { p.Path = "/a#b" }, func(p *Page) { p.UpdatedAt = p.CreatedAt.Add(-time.Second) }, func(p *Page) { p.Blocks[0].PageID = 2 }, func(p *Page) { p.Blocks = append(p.Blocks, p.Blocks[0]) }} {
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

func TestBlockResults(t *testing.T) {
	b := imported()
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	r := PreparedContent{SourceID: 1, SnapshotID: 1, PreparedAt: instant(), Rules: ExtractionRules{Start: TextAnchor{Text: "старый якорь"}}}
	b.Import.Rules = r.Rules
	if err := b.ApplyPrepared(r); err != nil {
		t.Fatal(err)
	}
	r.Content = "изменение копии"
	if b.Prepared.Content != "" {
		t.Fatal("результат не скопирован")
	}
	b.Import.Rules.Start.Text = "новый якорь"
	if err := b.Validate(); err != nil {
		t.Fatal("старый результат должен сохраняться", err)
	}
	old := b.Prepared
	requireInvalid(t, b.ApplyPrepared(PreparedContent{}))
	if b.Prepared != old {
		t.Fatal("ошибка заменила результат")
	}
	for _, bad := range []Block{{Type: BlockText, Import: &ImportSettings{}}, {Type: BlockText, Prepared: &r}, {Type: BlockImported}, {Type: "unknown"}, {Type: BlockText, ID: -1}, {Type: BlockImported, Import: &ImportSettings{SourceID: 1}, Text: "не допускается"}} {
		requireInvalid(t, bad.Validate())
	}
	r.PublishedAt = r.PreparedAt.Add(-time.Second)
	requireInvalid(t, r.Validate())
}

func TestSafeLogging(t *testing.T) {
	const secret = "CONFIDENTIAL_MARKER"
	b := imported()
	b.Import.Rules.Start.Text = secret
	b.Prepared = &PreparedContent{Content: secret}
	p := page()
	p.Title = secret
	p.Path = secret
	p.Blocks = []Block{b}
	cases := []struct {
		v    any      // Сущность для проверки безопасного логирования.
		keys []string // Ожидаемые атрибуты журнала.
	}{
		{Source{ID: 1, Name: secret, ExternalRef: secret, FetchParams: map[string]string{"token": secret}, Sync: SyncState{LastError: secret}}, []string{"id", "type", "auto_refresh", "publication_blocked", "sync_status"}},
		{Snapshot{ID: 1, RawContent: []byte(secret), Checksum: secret, Metadata: map[string]string{"secret": secret}}, []string{"id", "source_id", "raw_size"}},
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
