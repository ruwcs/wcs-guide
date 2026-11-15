package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// fixtureService создаёт временное хранилище аутентификации.
func fixtureService(t *testing.T) (*storage.Storage, *Service, string) {
	t.Helper()
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	origin, _ := url.Parse("https://example.org")
	service, err := New(store, *origin)
	if err != nil {
		t.Fatal(err)
	}
	_, code, err := Bootstrap(context.Background(), store, "owner", "Владелец")
	if err != nil {
		t.Fatal(err)
	}
	return store, service, code
}

// TestRegistrationVerification проверяет Origin, challenge, UV и погашение разрешения.
func TestRegistrationVerification(t *testing.T) {
	for _, kind := range []string{"valid", "origin", "challenge", "uv", "rp", "expired", "recovered"} {
		t.Run(kind, func(t *testing.T) {
			store, service, code := fixtureService(t)
			ctx := context.Background()
			id, _, err := service.Begin(ctx, "register", code, "")
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile("testdata/registration.json")
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]any
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			inner := response["response"].(map[string]any)
			clientBytes, err := base64.RawURLEncoding.DecodeString(inner["clientDataJSON"].(string))
			if err != nil {
				t.Fatal(err)
			}
			var client map[string]any
			json.Unmarshal(clientBytes, &client)
			service.mutex.Lock()
			op := service.operations[id]
			op.session.Challenge = client["challenge"].(string)
			if kind == "challenge" {
				op.session.Challenge = "wrong"
			}
			if kind == "expired" {
				op.expires = time.Now().Add(-time.Second)
			}
			service.operations[id] = op
			service.mutex.Unlock()
			if kind == "origin" {
				client["origin"] = "https://evil.example"
				raw, _ := json.Marshal(client)
				inner["clientDataJSON"] = base64.RawURLEncoding.EncodeToString(raw)
			}
			if kind == "uv" || kind == "rp" {
				att, _ := base64.RawURLEncoding.DecodeString(inner["attestationObject"].(string))
				hashOffset := bytes.Index(att, []byte{0xbf, 0xab, 0xc3, 0x74})
				if hashOffset < 0 {
					t.Fatal("не найден RP ID hash в fixture")
				}
				if kind == "uv" {
					att[hashOffset+32] &= ^byte(4)
				} else {
					att[hashOffset] ^= 1
				}
				inner["attestationObject"] = base64.RawURLEncoding.EncodeToString(att)
			}
			if kind == "recovered" {
				if _, _, err := Recover(ctx, store, "owner"); err != nil {
					t.Fatal(err)
				}
			}
			body, _ = json.Marshal(response)
			_, err = service.Finish(ctx, "register", id, httptest.NewRequest("POST", "https://example.org/auth/register/finish", bytes.NewReader(body)))
			if (err == nil) != (kind == "valid") {
				t.Fatalf("результат %s: %v", kind, err)
			}
			if _, err := service.Finish(ctx, "register", id, httptest.NewRequest("POST", "/", bytes.NewReader(body))); !errors.Is(err, ErrDenied) {
				t.Fatal("операция использована повторно", err)
			}
			if kind == "valid" {
				if _, _, err := service.Begin(ctx, "register", code, ""); err == nil {
					t.Fatal("повторное разрешение")
				}
			}
		})
	}
}

// TestSessionRecovery проверяет роли, сессии и восстановление доступа.
func TestSessionRecovery(t *testing.T) {
	store, _, code := fixtureService(t)
	ctx := context.Background()
	user, err := store.GetUserByLogin(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Bootstrap(ctx, store, "second", "Второй"); err == nil {
		t.Fatal("повторный bootstrap")
	}
	at := time.Now().UTC()
	credential := []byte("key")
	data := []byte(`{}`)
	if err := store.RegisterCredential(ctx, user.ID, Hash(code), credential, data, at); err != nil {
		t.Fatal(err)
	}
	token := "session"
	if err := store.LoginUser(ctx, user.ID, credential, data, data, Hash(token), at, at.Add(SessionLifetime)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserAccess(ctx, user.ID, domain.RoleEditor, true); err != nil {
		t.Fatal(err)
	}
	user, err = store.SessionUser(ctx, Hash(token), at)
	if err != nil || user.Role != domain.RoleEditor {
		t.Fatal(user, err)
	}
	if _, err := store.SessionUser(ctx, Hash(token), at.Add(SessionLifetime)); err == nil {
		t.Fatal("истечение сессии")
	}
	if _, _, err := Recover(ctx, store, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionUser(ctx, Hash(token), at); err == nil {
		t.Fatal("сессия не отозвана")
	}
	if err := store.LoginUser(ctx, user.ID, credential, data, data, Hash("stale"), at, at.Add(SessionLifetime)); err == nil {
		t.Fatal("вход отозванным ключом")
	}
	if err := store.RegisterCredential(ctx, user.ID, Hash(code), []byte("stale"), data, at); err == nil {
		t.Fatal("старое разрешение")
	}
	user, err = store.GetUserByLogin(ctx, "owner")
	if err != nil || !user.Active || user.Role != domain.RoleAdmin {
		t.Fatal(user, err)
	}
}

// TestConcurrentGrant проверяет одноразовое погашение разрешения.
func TestConcurrentGrant(t *testing.T) {
	store, _, code := fixtureService(t)
	ctx := context.Background()
	user, err := store.GetUserByLogin(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"one", "two"} {
		workers.Add(1)
		go func(id string) {
			defer workers.Done()
			results <- store.RegisterCredential(ctx, user.ID, Hash(code), []byte(id), []byte(`{}`), time.Now().UTC())
		}(id)
	}
	workers.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
}

// TestSessionPersistenceAndPermissions проверяет перезапуск, роли и блокировку.
func TestSessionPersistenceAndPermissions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "persist.db")
	store, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_, code, err := Bootstrap(ctx, store, "owner", "Владелец")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.GetUserByLogin(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data := []byte(`{}`)
	key := []byte("key")
	token := "session"
	if err := store.RegisterCredential(ctx, u.ID, Hash(code), key, data, now); err != nil {
		t.Fatal(err)
	}
	if err := store.LoginUser(ctx, u.ID, key, data, data, Hash(token), now, now.Add(SessionLifetime)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	origin, _ := url.Parse("https://example.org")
	service, err := New(store, *origin)
	if err != nil {
		t.Fatal(err)
	}
	allowed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	check := func(expected int) {
		t.Helper()
		r := httptest.NewRequest("POST", "/test", nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		service.RequireRole("session", domain.RoleAdmin, allowed).ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatal(w.Code)
		}
	}
	check(204)
	if err := store.SetUserAccess(ctx, u.ID, domain.RoleEditor, true); err != nil {
		t.Fatal(err)
	}
	check(403)
	if err := store.SetUserAccess(ctx, u.ID, domain.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	check(401)
	if err := store.LoginUser(ctx, u.ID, key, data, data, Hash("blocked"), now, now.Add(SessionLifetime)); err == nil {
		t.Fatal("вход заблокированного пользователя")
	}
}

// TestGrantExpiry проверяет истечение разрешения до сохранения ключа.
func TestGrantExpiry(t *testing.T) {
	store, _, code := fixtureService(t)
	ctx := context.Background()
	user, err := store.GetUserByLogin(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	afterExpiry := time.Now().UTC().Add(time.Hour)
	if _, err := store.GrantUser(ctx, Hash(code), afterExpiry); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("просроченное разрешение доступно", err)
	}
	if err := store.RegisterCredential(ctx, user.ID, Hash(code), []byte("expired"), []byte(`{}`), afterExpiry); err == nil {
		t.Fatal("просроченное разрешение погашено")
	}
	credentials, err := store.UserCredentials(ctx, user.ID)
	if err != nil || len(credentials) != 0 {
		t.Fatal("ключ сохранён после истечения разрешения", err)
	}
}

// TestLogout проверяет отзыв текущей сессии.
func TestLogout(t *testing.T) {
	store, service, code := fixtureService(t)
	ctx := context.Background()
	user, err := store.GetUserByLogin(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	data, key := []byte(`{}`), []byte("key")
	if err := store.RegisterCredential(ctx, user.ID, Hash(code), key, data, at); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"current", "other"} {
		if err := store.LoginUser(ctx, user.ID, key, data, data, Hash(token), at, at.Add(SessionLifetime)); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.Logout(ctx, "current"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SessionUser(ctx, "current"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("сессия не отозвана", err)
	}
	if _, err := service.SessionUser(ctx, "other"); err != nil {
		t.Fatal("отозвана чужая сессия", err)
	}
}

// TestGrantAcrossConnections проверяет атомарность регистрации через два подключения.
func TestGrantAcrossConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	_, code, err := Bootstrap(ctx, first, "owner", "Владелец")
	if err != nil {
		t.Fatal(err)
	}
	second, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	user, err := first.GetUserByLogin(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	for i, store := range []*storage.Storage{first, second} {
		go func() {
			<-start
			results <- store.RegisterCredential(ctx, user.ID, Hash(code), []byte{byte(i + 1)}, []byte(`{}`), time.Now().UTC())
		}()
	}
	close(start)
	success := 0
	for range 2 {
		if err := <-results; err == nil {
			success++
		} else if !errors.Is(err, storage.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("число успешных регистраций", success)
	}
}
