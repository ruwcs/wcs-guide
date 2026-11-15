// Package auth управляет входом пользователей посредством Passkeys.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/ruwcs/wcs-guide/internal/storage"
)

// grantLifetime — срок разрешения регистрации.
const grantLifetime = 30 * time.Minute

// operationLifetime — срок операции WebAuthn.
const operationLifetime = 5 * time.Minute

// SessionLifetime — срок сессии пользователя.
const SessionLifetime = 30 * 24 * time.Hour

// ErrDenied — отказ аутентификации.
var ErrDenied = errors.New("auth: операция отклонена")

// ErrLimited — превышение ограничения операций.
var ErrLimited = errors.New("auth: слишком много операций")

// maxOperations — максимальное число незавершённых операций.
const maxOperations = 128

// authRequestsPerMinute — предел запросов аутентификации за минуту.
const authRequestsPerMinute = 60

// Service — аутентификация пользователей редакторского интерфейса.
type Service struct {
	store      *storage.Storage     // Хранилище.
	webauthn   *webauthn.WebAuthn   // Проверка WebAuthn.
	mutex      sync.Mutex           // Защита временных операций.
	operations map[string]operation // Незавершённые операции браузеров.
	window     time.Time            // Начало окна ограничения запросов.
	requests   int                  // Запросы текущего окна.
}

// operation — незавершённая регистрация или вход.
type operation struct {
	kind     string               // Вид операции.
	user     webUser              // Пользователь операции.
	session  webauthn.SessionData // Проверяемые данные WebAuthn.
	grant    []byte               // Хеш регистрационного разрешения.
	expires  time.Time            // Окончание срока операции.
	previous map[string][]byte    // Проверенные данные ключей до входа.
}

// webUser — представление пользователя для WebAuthn.
type webUser struct {
	user        domain.User           // Пользователь приложения.
	credentials []webauthn.Credential // Зарегистрированные ключи доступа.
}

// WebAuthnID реализует интерфейс webauthn.User для webUser.
func (u webUser) WebAuthnID() []byte { return u.user.WebAuthnID }

// WebAuthnName реализует интерфейс webauthn.User для webUser.
func (u webUser) WebAuthnName() string { return u.user.Login }

// WebAuthnDisplayName реализует интерфейс webauthn.User для webUser.
func (u webUser) WebAuthnDisplayName() string { return u.user.DisplayName }

// WebAuthnCredentials реализует интерфейс webauthn.User для webUser.
func (u webUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// New возвращает службу аутентификации.
func New(store *storage.Storage, origin url.URL) (*Service, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          origin.Hostname(),
		RPDisplayName: "WCS Guide Editor",
		RPOrigins:     []string{origin.String()},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			UserVerification: protocol.VerificationRequired,
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
		},
		AttestationPreference: protocol.PreferNoAttestation,
	})
	if err != nil {
		return nil, err
	}
	return &Service{store: store, webauthn: wa, operations: make(map[string]operation)}, nil
}

// randomToken возвращает случайный секретный токен.
func randomToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// Hash возвращает хеш токена.
func Hash(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }

// Bootstrap создаёт первого администратора.
func Bootstrap(ctx context.Context, store *storage.Storage, login, name string) (int64, string, error) {
	token, err := randomToken()
	if err != nil {
		return 0, "", err
	}
	handle := make([]byte, 32)
	if _, err = rand.Read(handle); err != nil {
		return 0, "", err
	}
	at := time.Now().UTC()
	id, err := store.BootstrapAdmin(ctx, domain.User{Login: strings.ToLower(login), DisplayName: name, WebAuthnID: handle, Role: domain.RoleAdmin, Active: true, CreatedAt: at}, Hash(token), at.Add(grantLifetime))
	if err != nil {
		return 0, "", err
	}
	return id, token, nil
}

// Recover восстанавливает административный доступ.
func Recover(ctx context.Context, store *storage.Storage, login string) (int64, string, error) {
	token, err := randomToken()
	if err != nil {
		return 0, "", err
	}
	at := time.Now().UTC()
	id, err := store.RecoverAdmin(ctx, strings.ToLower(login), Hash(token), at, at.Add(grantLifetime))
	if err != nil {
		return 0, "", err
	}
	return id, token, nil
}

// allow проверяет ограничение запросов.
func (s *Service) allow() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := time.Now()
	if now.Sub(s.window) >= time.Minute {
		s.window = now
		s.requests = 0
	}
	s.requests++
	return s.requests <= authRequestsPerMinute
}

// Begin начинает регистрацию или вход.
func (s *Service) Begin(ctx context.Context, kind, value, oldID string) (string, any, error) {
	if !s.allow() {
		return "", nil, ErrLimited
	}
	var u domain.User
	var err error
	var grant []byte
	if kind == "register" {
		grant = Hash(value)
		u, err = s.store.GrantUser(ctx, grant, time.Now().UTC())
	} else if kind == "login" {
		u, err = s.store.GetUserByLogin(ctx, strings.ToLower(value))
	} else {
		return "", nil, ErrDenied
	}
	if errors.Is(err, storage.ErrNotFound) || (err == nil && !u.Active) {
		return "", nil, ErrDenied
	}
	if err != nil {
		return "", nil, err
	}
	data, err := s.store.UserCredentials(ctx, u.ID)
	if err != nil {
		return "", nil, err
	}
	user := webUser{user: u}
	previous := make(map[string][]byte)
	for _, raw := range data {
		var credential webauthn.Credential
		if err := json.Unmarshal(raw, &credential); err != nil {
			return "", nil, err
		}
		user.credentials = append(user.credentials, credential)
		previous[string(credential.ID)] = raw
	}
	var options any
	var session *webauthn.SessionData
	if kind == "register" {
		options, session, err = s.webauthn.BeginRegistration(user)
	} else {
		if len(user.credentials) == 0 {
			return "", nil, ErrDenied
		}
		options, session, err = s.webauthn.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired))
	}
	if err != nil {
		return "", nil, ErrDenied
	}
	id, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := time.Now()
	for key, op := range s.operations {
		if !op.expires.After(now) {
			delete(s.operations, key)
		}
	}
	delete(s.operations, oldID)
	if len(s.operations) >= maxOperations {
		return "", nil, ErrLimited
	}
	s.operations[id] = operation{kind: kind, user: user, session: *session, grant: grant, expires: now.Add(operationLifetime), previous: previous}
	return id, options, nil
}

// Finish завершает регистрацию или вход.
func (s *Service) Finish(ctx context.Context, kind, id string, r *http.Request) (string, error) {
	if !s.allow() {
		return "", ErrLimited
	}
	s.mutex.Lock()
	op, ok := s.operations[id]
	delete(s.operations, id)
	s.mutex.Unlock()
	if !ok || op.kind != kind || !op.expires.After(time.Now()) {
		return "", ErrDenied
	}
	var credential *webauthn.Credential
	var err error
	if kind == "register" {
		credential, err = s.webauthn.FinishRegistration(op.user, op.session, r)
	} else {
		credential, err = s.webauthn.FinishLogin(op.user, op.session, r)
	}
	if err != nil || credential.Authenticator.CloneWarning {
		return "", ErrDenied
	}
	data, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if kind == "register" {
		return "", s.store.RegisterCredential(ctx, op.user.user.ID, op.grant, credential.ID, data, now)
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	err = s.store.LoginUser(ctx, op.user.user.ID, credential.ID, op.previous[string(credential.ID)], data, Hash(token), now, now.Add(SessionLifetime))
	if err != nil {
		return "", err
	}
	return token, nil
}

// SessionUser возвращает пользователя сессии.
func (s *Service) SessionUser(ctx context.Context, token string) (domain.User, error) {
	return s.store.SessionUser(ctx, Hash(token), time.Now().UTC())
}

// Logout завершает сессию пользователя.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.Logout(ctx, Hash(token))
}

// RequireRole защищает обработчик правами пользователя.
func (s *Service) RequireRole(cookie string, role domain.Role, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookie)
		if err != nil {
			http.Error(w, "Требуется вход", 401)
			return
		}
		u, err := s.SessionUser(r.Context(), c.Value)
		if errors.Is(err, storage.ErrNotFound) {
			http.Error(w, "Требуется вход", 401)
			return
		}
		if err != nil {
			http.Error(w, "Операция не выполнена", 500)
			return
		}
		if role == domain.RoleAdmin && u.Role != domain.RoleAdmin {
			http.Error(w, "Недостаточно прав", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}
