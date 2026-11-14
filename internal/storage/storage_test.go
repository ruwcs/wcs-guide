package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

var ctx = context.Background()

func clock() time.Time {
	return time.Date(2026, 11, 14, 12, 0, 0, 123456789, time.FixedZone("test", 10800))
}
func openTest(t *testing.T) (*Storage, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func wantError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("получено %v, ожидалось %v", err, want)
	}
}
func makeSource(t *testing.T, s *Storage, ref string) SourceRecord {
	t.Helper()
	r, err := s.CreateSource(ctx, domain.Source{Name: "Название", Type: domain.SourceTelegramPost, ExternalRef: ref, FetchParams: map[string]string{"secret": "TOKEN"}})
	must(t, err)
	return r
}
func fetch(t *testing.T, s *Storage, id int64, sum string, at time.Time) (domain.Snapshot, bool) {
	t.Helper()
	_, token, err := s.GetFetchInput(ctx, id)
	must(t, err)
	v, created, err := s.SaveFetch(ctx, token, domain.Snapshot{SourceID: id, RawContent: []byte{0, 255, 1}, Format: "test", Checksum: sum, Metadata: map[string]string{}, FetchedAt: at, SourceModifiedAt: at.Add(-time.Minute)}, at)
	must(t, err)
	return v, created
}
func makePage(t *testing.T, s *Storage, sid int64) PageRecord {
	t.Helper()
	p := domain.Page{Path: "/календарь", Title: "Календарь", Language: "ru", CreatedAt: clock(), UpdatedAt: clock(), Blocks: []domain.Block{{Type: domain.BlockText, Text: "Введение"}, {Type: domain.BlockImported, Import: &domain.ImportSettings{SourceID: sid}}}}
	r, err := s.CreatePage(ctx, p)
	must(t, err)
	return r
}
func prepare(t *testing.T, s *Storage, bid int64, settings domain.ImportSettings, body string) (PreparationToken, domain.PreparedContent) {
	t.Helper()
	in, err := s.GetPreparation(ctx, bid, settings)
	must(t, err)
	return in.Token, domain.PreparedContent{SourceID: settings.SourceID, SnapshotID: in.Snapshot.ID, Content: body, Rules: settings.Rules, PreparedAt: clock().Add(time.Hour)}
}

func TestOpenAndPersistence(t *testing.T) {
	s, path := openTest(t)
	r := makeSource(t, s, "publication")
	snap, _ := fetch(t, s, r.Source.ID, "A", clock())
	must(t, s.Close())
	s, err := Open(ctx, path)
	must(t, err)
	defer s.Close()
	got, err := s.GetSnapshot(ctx, snap.ID)
	must(t, err)
	if !reflect.DeepEqual(got.RawContent, snap.RawContent) || !got.FetchedAt.Equal(clock()) || got.FetchedAt.Location() != time.UTC || got.Metadata == nil {
		t.Fatal(got)
	}
	src, err := s.GetSource(ctx, r.Source.ID)
	must(t, err)
	if src.Source.FetchParams["secret"] != "TOKEN" {
		t.Fatal(src)
	}
	must(t, s.checkPragmas(ctx))
	conn, err := s.db.Conn(ctx)
	must(t, err)
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	conn.Close()
	must(t, s.checkPragmas(ctx))
}
func TestSchemaAndForeignKeys(t *testing.T) {
	s, path := openTest(t)
	_, err := s.db.ExecContext(ctx, "INSERT INTO snapshots(source_id,raw_content,format,checksum,fetched_at) VALUES(999,x'01','f','c','t')")
	wantError(t, protect(err), ErrConflict)
	a := makeSource(t, s, "identity")
	_, err = s.db.ExecContext(ctx, "UPDATE sources SET external_ref='changed' WHERE id=?", a.Source.ID)
	wantError(t, protect(err), ErrConflict)
	fetch(t, s, a.Source.ID, "A", clock())
	_, err = s.db.ExecContext(ctx, "DELETE FROM sources WHERE id=?", a.Source.ID)
	wantError(t, protect(err), ErrConflict)
	_, err = s.db.ExecContext(ctx, "PRAGMA user_version=0")
	must(t, err)
	must(t, s.Close())
	_, err = Open(ctx, path)
	wantError(t, err, ErrSchemaIncompatible)
	s2, path2 := openTest(t)
	_, err = s2.db.ExecContext(ctx, "PRAGMA user_version=999")
	must(t, err)
	must(t, s2.Close())
	_, err = Open(ctx, path2)
	wantError(t, err, ErrSchemaTooNew)
	s3, _ := openTest(t)
	err = s3.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE TABLE rollback_test(id INTEGER); INVALID SQL")
		return err
	})
	if err == nil {
		t.Fatal("ожидался откат")
	}
	var n int
	must(t, s3.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name='rollback_test'").Scan(&n))
	if n != 0 {
		t.Fatal("частичная схема")
	}
}
func TestFetchVersionsAndTokens(t *testing.T) {
	s, _ := openTest(t)
	src := makeSource(t, s, "A")
	id := src.Source.ID
	_, token, err := s.GetFetchInput(ctx, id)
	must(t, err)
	a, yes := fetch(t, s, id, "A", clock())
	if !yes {
		t.Fatal("нет снимка")
	}
	same, yes := fetch(t, s, id, "A", clock().Add(time.Second))
	if yes || same.ID != a.ID {
		t.Fatal("дубликат")
	}
	fetch(t, s, id, "B", clock().Add(2*time.Second))
	again, yes := fetch(t, s, id, "A", clock().Add(3*time.Second))
	if !yes || again.ID == a.ID {
		t.Fatal("потерян переход A-B-A")
	}
	wantError(t, s.SaveFetchFailure(ctx, token, clock().Add(time.Hour), "ошибка"), ErrConflict)
	r, err := s.GetSource(ctx, id)
	must(t, err)
	v := r.Source
	v.Name = "Изменено"
	v.Sync = domain.SyncState{}
	_, err = s.UpdateSource(ctx, v, r.EditRevision, clock())
	must(t, err)
	r, err = s.GetSource(ctx, id)
	must(t, err)
	if r.Source.Sync.LastSuccessAt.IsZero() {
		t.Fatal("потеряно состояние")
	}
	must(t, s.SetAutoRefresh(ctx, id, true, time.Second))
	due, err := s.ListDueSources(ctx, clock().Add(time.Hour))
	must(t, err)
	if len(due) != 1 {
		t.Fatal(due)
	}
	_, ft, err := s.GetFetchInput(ctx, id)
	must(t, err)
	must(t, s.SaveFetchFailure(ctx, ft, clock().Add(time.Hour), "ошибка"))
	r, err = s.GetSource(ctx, id)
	must(t, err)
	if r.Source.Name != "Изменено" || !r.Source.AutoRefresh || r.Source.Sync.LastSuccessAt.IsZero() {
		t.Fatal(r)
	}
}
func TestPagesAndConcurrentResults(t *testing.T) {
	s, _ := openTest(t)
	src := makeSource(t, s, "one")
	fetch(t, s, src.Source.ID, "A", clock())
	p := makePage(t, s, src.Source.ID)
	bid := p.Page.Blocks[1].ID
	settings := *p.Page.Blocks[1].Import
	_, err := s.GetPublicPage(ctx, p.Page.Path)
	wantError(t, err, ErrNotFound)
	token, result := prepare(t, s, bid, settings, "Содержимое")
	must(t, s.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	must(t, s.PublishPage(ctx, p.Page.ID, p.EditRevision, clock().Add(3*time.Hour)))
	public, err := s.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	if len(public.Blocks) != 2 || public.Blocks[1].ExternalRef != "one" {
		t.Fatal(public)
	}
	oldEditor, err := s.GetPage(ctx, p.Page.ID)
	must(t, err)
	fetch(t, s, src.Source.ID, "B", clock().Add(time.Minute))
	token, result = prepare(t, s, bid, settings, "Содержимое")
	must(t, s.ApplyPrepared(ctx, token, result, clock().Add(4*time.Hour)))
	same, err := s.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	if !same.UpdatedAt.Equal(public.UpdatedAt) {
		t.Fatal("ложная дата обновления")
	}
	wantError(t, s.ApplyPrepared(ctx, token, result, clock().Add(5*time.Hour)), ErrConflict)
	oldEditor.Page.Title = "Новый заголовок"
	oldEditor.Page.Blocks[0], oldEditor.Page.Blocks[1] = oldEditor.Page.Blocks[1], oldEditor.Page.Blocks[0]
	saved, err := s.SavePage(ctx, oldEditor.Page, oldEditor.EditRevision, clock().Add(5*time.Hour))
	must(t, err)
	if saved.Page.Blocks[0].ID != bid || saved.Page.Blocks[0].Prepared.SnapshotID != result.SnapshotID {
		t.Fatal("потерян результат или порядок")
	}
	token, result = prepare(t, s, bid, settings, "новый текст")
	fetch(t, s, src.Source.ID, "C", clock().Add(2*time.Minute))
	wantError(t, s.ApplyPrepared(ctx, token, result, clock().Add(6*time.Hour)), ErrConflict)
	rules := settings.Rules
	rules.Start.Text = "якорь"
	must(t, s.SetImportRules(ctx, bid, saved.BlockRevisions[bid], rules))
	current, err := s.GetPage(ctx, p.Page.ID)
	must(t, err)
	must(t, s.SetImportRules(ctx, bid, current.BlockRevisions[bid], settings.Rules))
	wantError(t, s.ApplyPrepared(ctx, token, result, clock().Add(6*time.Hour)), ErrConflict)
	current, err = s.GetPage(ctx, p.Page.ID)
	must(t, err)
	if current.Page.Blocks[0].Prepared == nil {
		t.Fatal("потерян прежний результат")
	}
	second := makeSource(t, s, "two")
	fetch(t, s, second.Source.ID, "new", clock())
	newSettings := domain.ImportSettings{SourceID: second.Source.ID}
	token, result = prepare(t, s, bid, newSettings, "другой источник")
	must(t, s.ReplaceImport(ctx, token, result, clock().Add(7*time.Hour)))
	public, err = s.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	if public.Blocks[0].ExternalRef != "two" {
		t.Fatal("неверная атрибуция")
	}
	must(t, s.SetPublicationBlocked(ctx, second.Source.ID, true, clock().Add(8*time.Hour)))
	hidden, err := s.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	if len(hidden.Blocks) != 1 {
		t.Fatal("запрет обойдён")
	}
	must(t, s.SetPublicationBlocked(ctx, second.Source.ID, false, clock().Add(9*time.Hour)))
	shown, err := s.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	if len(shown.Blocks) != 2 || !shown.UpdatedAt.After(public.UpdatedAt) {
		t.Fatal(shown)
	}
	current, err = s.GetPage(ctx, p.Page.ID)
	must(t, err)
	must(t, s.UnpublishPage(ctx, p.Page.ID, current.EditRevision, clock().Add(10*time.Hour)))
	_, err = s.GetPublicPage(ctx, p.Page.Path)
	wantError(t, err, ErrNotFound)
}
func TestUniquePathsAndRollback(t *testing.T) {
	s, _ := openTest(t)
	src := makeSource(t, s, "source")
	p := makePage(t, s, src.Source.ID)
	_, err := s.CreatePage(ctx, domain.Page{Path: p.Page.Path, CreatedAt: clock(), UpdatedAt: clock()})
	wantError(t, err, ErrUnique)
	for range 2 {
		_, err = s.CreatePage(ctx, domain.Page{CreatedAt: clock(), UpdatedAt: clock()})
		must(t, err)
	}
	before, err := s.GetPage(ctx, p.Page.ID)
	must(t, err)
	bad := before.Page
	bad.Blocks = append(append([]domain.Block(nil), bad.Blocks...), domain.Block{PageID: p.Page.ID, Type: domain.BlockImported, Import: &domain.ImportSettings{SourceID: 999}})
	_, err = s.SavePage(ctx, bad, before.EditRevision, clock().Add(time.Hour))
	wantError(t, err, ErrConflict)
	after, err := s.GetPage(ctx, p.Page.ID)
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("транзакция не откатилась")
	}
	c, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.GetPage(c, p.Page.ID)
	wantError(t, err, context.Canceled)
	conn, err := s.db.Conn(ctx)
	must(t, err)
	c, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	_, err = s.GetPage(c, p.Page.ID)
	wantError(t, err, context.DeadlineExceeded)
	conn.Close()
}
func TestTimeMapsAndSafeErrors(t *testing.T) {
	s, _ := openTest(t)
	for _, m := range []map[string]string{nil, {}, {"ключ": "значение"}} {
		r, err := s.CreateSource(ctx, domain.Source{Name: "имя", Type: domain.SourceTelegramPost, ExternalRef: time.Now().String(), FetchParams: m})
		must(t, err)
		if !reflect.DeepEqual(m, r.Source.FetchParams) {
			t.Fatal("карта изменилась")
		}
	}
	for _, at := range []time.Time{time.Date(1500, 1, 1, 0, 0, 0, 1, time.UTC), clock()} {
		r := makeSource(t, s, at.String())
		v, _ := fetch(t, s, r.Source.ID, "sum", at)
		if !v.FetchedAt.Equal(at) {
			t.Fatal(v)
		}
	}
	path := filepath.Join(t.TempDir(), "unknown.db")
	db, err := sql.Open("sqlite", path)
	must(t, err)
	_, err = db.Exec("CREATE TABLE unknown(id INTEGER)")
	must(t, err)
	db.Close()
	_, err = Open(ctx, path)
	wantError(t, err, ErrSchemaIncompatible)
	text := protect(errors.New("SECRET payload")).Error()
	if strings.Contains(text, "SECRET") {
		t.Fatal(text)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(path), "unknown.db-wal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("неожиданный WAL")
	}
}

func TestBlockedPreparationAndABA(t *testing.T) {
	s, _ := openTest(t)
	src := makeSource(t, s, "blocked")
	fetch(t, s, src.Source.ID, "A", clock())
	p := makePage(t, s, src.Source.ID)
	bid := p.Page.Blocks[1].ID
	settings := *p.Page.Blocks[1].Import
	token, result := prepare(t, s, bid, settings, "текст")
	current, err := s.GetPage(ctx, p.Page.ID)
	must(t, err)
	rules := settings.Rules
	rules.End.Text = "граница"
	must(t, s.SetImportRules(ctx, bid, current.BlockRevisions[bid], rules))
	current, err = s.GetPage(ctx, p.Page.ID)
	must(t, err)
	must(t, s.SetImportRules(ctx, bid, current.BlockRevisions[bid], settings.Rules))
	wantError(t, s.ApplyPrepared(ctx, token, result, clock().Add(time.Hour)), ErrConflict)
	must(t, s.SetPublicationBlocked(ctx, src.Source.ID, true, clock()))
	token, result = prepare(t, s, bid, settings, "текст")
	must(t, s.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	current, err = s.GetPage(ctx, p.Page.ID)
	must(t, err)
	must(t, s.PublishPage(ctx, p.Page.ID, current.EditRevision, clock().Add(3*time.Hour)))
	hidden, err := s.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	if len(hidden.Blocks) != 1 {
		t.Fatal(hidden)
	}
	must(t, s.SetPublicationBlocked(ctx, src.Source.ID, false, clock().Add(4*time.Hour)))
	shown, err := s.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	if len(shown.Blocks) != 2 {
		t.Fatal(shown)
	}
	current, err = s.GetPage(ctx, p.Page.ID)
	must(t, err)
	if current.Page.Blocks[1].Prepared.PublishedAt.IsZero() {
		t.Fatal("не записана публикация")
	}
}

func TestExternalLockAndFailedBootstrap(t *testing.T) {
	s, path := openTest(t)
	other, err := Open(ctx, path)
	must(t, err)
	defer other.Close()
	_, err = other.db.ExecContext(ctx, "PRAGMA busy_timeout=20")
	must(t, err)
	tx, err := s.db.BeginTx(ctx, nil)
	must(t, err)
	_, err = other.CreateSource(ctx, domain.Source{Name: "имя", Type: domain.SourceTelegramPost, ExternalRef: "ref"})
	wantError(t, err, ErrConflict)
	must(t, tx.Rollback())
	badPath := filepath.Join(t.TempDir(), "bad.db")
	db, err := sql.Open("sqlite", badPath)
	must(t, err)
	st := &Storage{db: db}
	defer db.Close()
	initial, err := migrations.ReadFile("migrations/001_initial.sql")
	must(t, err)
	// Сбой встроенного скрипта проверяется на отдельной БД до первого открытия.
	err = st.withTx(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, string(initial)+";INVALID SQL;"); return err })
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	var n int
	must(t, db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&n))
	if n != 0 {
		t.Fatal("схема сохранилась частично")
	}
}

func TestCompletePageRoundTripAndProvenance(t *testing.T) {
	s, path := openTest(t)
	src := makeSource(t, s, "roundtrip")
	snap, _ := fetch(t, s, src.Source.ID, "A", clock())
	p := makePage(t, s, src.Source.ID)
	bid := p.Page.Blocks[1].ID
	settings := *p.Page.Blocks[1].Import
	token, result := prepare(t, s, bid, settings, "полезный текст")
	must(t, s.ApplyPrepared(ctx, token, result, clock().Add(2*time.Hour)))
	must(t, s.PublishPage(ctx, p.Page.ID, p.EditRevision, clock().Add(3*time.Hour)))
	before, err := s.GetPage(ctx, p.Page.ID)
	must(t, err)
	pub, err := s.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	other := makeSource(t, s, "other")
	otherSnap, _ := fetch(t, s, other.Source.ID, "B", clock())
	// Составной FK запрещает результату ссылаться на снимок другого источника.
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE prepared_contents SET snapshot_id=? WHERE block_id=?", otherSnap.ID, bid)
		return err
	})
	wantError(t, err, ErrConflict)
	must(t, s.Close())
	again, err := Open(ctx, path)
	must(t, err)
	defer again.Close()
	after, err := again.GetPage(ctx, p.Page.ID)
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("страница изменилась при открытии")
	}
	restored, err := again.GetPublicPage(ctx, p.Page.Path)
	must(t, err)
	if !reflect.DeepEqual(pub, restored) {
		t.Fatal("публичное представление изменилось")
	}
	if after.Page.Blocks[1].Prepared.SnapshotID != snap.ID {
		t.Fatal("нарушено происхождение")
	}
	info, err := os.Stat(path)
	must(t, err)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("права новой БД: %v", info.Mode().Perm())
	}
}
