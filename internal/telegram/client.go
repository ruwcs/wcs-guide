package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/log/logslog"
	"github.com/gotd/td/session"
	td "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/ruwcs/wcs-guide/internal/domain"
)

// Client — клиент получения публикаций Telegram.
type Client struct {
	pausedUntil time.Time     // Окончание обязательной паузы Telegram.
	apiID       int           // Идентификатор приложения Telegram.
	apiHash     string        // Секрет приложения Telegram.
	token       string        // Секрет отдельного бота.
	sessionPath string        // Путь к файлу сессии Telegram.
	mutex       sync.RWMutex  // Защита текущего подключения.
	api         *tg.Client    // Авторизованный клиент Telegram.
	ready       chan struct{} // Готовность авторизованного подключения.
	readyOnce   sync.Once     // Однократное уведомление о готовности.
	managed     bool          // Подключение запущено методом Run.
}

// Error — ошибка подключения к Telegram или получения публикации.
type Error struct {
	Code       string // Категория ошибки получения.
	RetryAfter int    // Пауза FLOOD_WAIT в секундах; ноль, если неизвестна.
}

// Error реализует интерфейс error для Error.
func (err *Error) Error() string { return "telegram: " + err.Code }

// New возвращает новый клиент Telegram.
func New(apiID int, apiHash, token, sessionPath string) *Client {
	return &Client{apiID: apiID, apiHash: apiHash, token: token, sessionPath: sessionPath, ready: make(chan struct{})}
}

// rpcError классифицирует ошибку Telegram.
func rpcError(err error) error {
	var remote *Error
	if errors.As(err, &remote) {
		return remote
	}
	if rpc, ok := tgerr.As(err); ok {
		if rpc.Type == "FLOOD_WAIT" {
			return &Error{Code: "rate_limit", RetryAfter: rpc.Argument}
		}
		switch rpc.Type {
		case "API_ID_INVALID", "API_ID_PUBLISHED_FLOOD", "ACCESS_TOKEN_INVALID", "ACCESS_TOKEN_EXPIRED", "AUTH_KEY_DUPLICATED":
			return &Error{Code: rpc.Type}
		}
		if rpc.Code == 401 {
			return &Error{Code: "authorization"}
		}
	}
	return &Error{Code: "unavailable"}
}

// Fetch получает публикацию Telegram.
func (c *Client) Fetch(ctx context.Context, source domain.Source) (Publication, error) {
	var result Publication
	username, id, err := ParsePost(source.ExternalRef)
	if err != nil {
		return result, &Error{Code: "invalid_reference"}
	}
	expected, err := strconv.ParseInt(source.FetchParams["telegram_channel_id"], 10, 64)
	if err != nil || expected <= 0 || source.FetchParams["telegram_message_id"] != strconv.Itoa(id) {
		return result, &Error{Code: "identity_not_configured"}
	}
	err = c.withAPI(ctx, func(ctx context.Context, api *tg.Client) error {

		peer, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			return rpcError(err)
		}
		actual, ok := peer.Peer.(*tg.PeerChannel)
		if !ok || actual.ChannelID != expected {
			return &Error{Code: "identity_changed"}
		}
		var channel *tg.Channel
		for _, item := range peer.Chats {
			if value, ok := item.(*tg.Channel); ok && value.ID == expected {
				channel = value
			}
		}
		if channel == nil {
			return &Error{Code: "unavailable"}
		}
		if channel.Noforwards || channel.Restricted {
			return &Error{Code: "publication_restricted"}
		}
		response, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}, ID: []tg.InputMessageClass{&tg.InputMessageID{ID: id}}})
		if err != nil {
			return rpcError(err)
		}
		modified, ok := response.AsModified()
		if !ok {
			return &Error{Code: "invalid_response"}
		}
		for _, item := range modified.GetMessages() {
			message, ok := item.(*tg.Message)
			if !ok || message.ID != id {
				continue
			}
			origin, ok := message.PeerID.(*tg.PeerChannel)
			if !ok || origin.ChannelID != expected {
				return &Error{Code: "identity_changed"}
			}
			if message.Noforwards || len(message.RestrictionReason) != 0 {
				return &Error{Code: "publication_restricted"}
			}
			if message.TTLPeriod != 0 {
				return &Error{Code: "ephemeral_unsupported"}
			}
			if message.Media != nil {
				if _, ok := message.Media.(*tg.MessageMediaWebPage); !ok {
					return &Error{Code: "media_unsupported"}
				}
			}
			result = Publication{ChannelID: expected, MessageID: id, Text: message.Message, CreatedAt: message.Date, EditedAt: message.EditDate}
			if rich, ok := message.GetRichMessage(); ok {
				if rich.Part {
					return &Error{Code: "rich_partial_unsupported"}
				}
				nodes, err := richNodes(rich.Blocks, 0)
				if err != nil {
					return &Error{Code: "rich_unsupported"}
				}
				result.Rich = nodes
			} else {
				for _, original := range message.Entities {
					switch original.(type) {
					case *tg.MessageEntityTextURL, *tg.MessageEntityURL, *tg.MessageEntityBold, *tg.MessageEntityItalic, *tg.MessageEntityStrike, *tg.MessageEntityUnderline, *tg.MessageEntityCode, *tg.MessageEntityPre, *tg.MessageEntityBlockquote, *tg.MessageEntitySpoiler, *tg.MessageEntityCustomEmoji:
					default:
						return &Error{Code: "entity_unsupported"}
					}
					item := Entity{Kind: original.TypeName(), Offset: original.GetOffset(), Length: original.GetLength()}
					switch value := original.(type) {
					case *tg.MessageEntityTextURL:
						item.URL = value.URL
					case *tg.MessageEntityPre:
						item.Language = value.Language
					case *tg.MessageEntityCustomEmoji:
						item.DocumentID = value.DocumentID
					case *tg.MessageEntityBlockquote:
						item.Collapsed = value.Collapsed
					}
					result.Entities = append(result.Entities, item)
				}
			}
			return nil
		}
		return &Error{Code: "missing_unconfirmed"}
	})
	return result, err
}

// authorizeBot авторизует бота Telegram.
func authorizeBot(ctx context.Context, client botAuthorizer, token string) error {
	status, err := client.Status(ctx)
	if err != nil {
		return rpcError(err)
	}
	if !status.Authorized {
		authorization, err := client.Bot(ctx, token)
		if err != nil {
			return rpcError(err)
		}
		user, ok := authorization.User.(*tg.User)
		if !ok {
			return &Error{Code: "session_identity"}
		}
		status.User = user
	}
	return validateBot(status.User, token)
}

// validateBot проверяет соответствие авторизованного бота токену.
func validateBot(user *tg.User, token string) error {
	expected, err := strconv.ParseInt(strings.SplitN(token, ":", 2)[0], 10, 64)
	if err != nil || user == nil || !user.Bot || user.ID != expected {
		return &Error{Code: "session_identity"}
	}
	return nil
}

// withAPI выполняет запрос к Telegram.
func (c *Client) withAPI(ctx context.Context, operation func(context.Context, *tg.Client) error) error {
	c.mutex.RLock()
	api := c.api
	managed := c.managed
	c.mutex.RUnlock()
	if api != nil {
		return operation(ctx, api)
	}
	if managed {
		return &Error{Code: "unavailable"}
	}
	client := td.NewClient(c.apiID, c.apiHash, td.Options{NoUpdates: true, Middlewares: []td.Middleware{td.MiddlewareFunc(c.rateLimit)}, SessionStorage: &session.FileStorage{Path: c.sessionPath}})
	var err error
	err = client.Run(ctx, func(ctx context.Context) error {
		if err := authorizeBot(ctx, client.Auth(), c.token); err != nil {
			return err
		}
		return operation(ctx, client.API())
	})
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var remote *Error
	if errors.As(err, &remote) {
		return remote
	}
	return &Error{Code: "unavailable"}
}

// Resolve возвращает идентификаторы канала и сообщения по адресу публикации.
func (c *Client) Resolve(ctx context.Context, reference string) (channelID int64, messageID int, err error) {
	username, id, err := ParsePost(reference)
	if err != nil {
		return 0, 0, &Error{Code: "invalid_reference"}
	}
	err = c.withAPI(ctx, func(ctx context.Context, api *tg.Client) error {
		peer, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
		if err != nil {
			return rpcError(err)
		}
		channel, ok := peer.Peer.(*tg.PeerChannel)
		if !ok {
			return &Error{Code: "invalid_reference"}
		}
		channelID = channel.ChannelID
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return channelID, id, nil
}

// authRetryInterval — интервал повторной авторизации Telegram.
const authRetryInterval = 5 * time.Minute

// Run поддерживает подключение к Telegram.
func (c *Client) Run(ctx context.Context, logger *slog.Logger) {
	c.mutex.Lock()
	c.managed = true
	c.mutex.Unlock()
	for ctx.Err() == nil {
		client := td.NewClient(c.apiID, c.apiHash, td.Options{NoUpdates: true, Middlewares: []td.Middleware{td.MiddlewareFunc(c.rateLimit)}, Logger: logslog.New(logger), SessionStorage: &session.FileStorage{Path: c.sessionPath}, OnAuthLost: func(ctx context.Context, err error) error { return err }})
		err := client.Run(ctx, func(ctx context.Context) error {
			if err := authorizeBot(ctx, client.Auth(), c.token); err != nil {
				return err
			}
			c.mutex.Lock()
			c.api = client.API()
			c.mutex.Unlock()
			c.readyOnce.Do(func() { close(c.ready) })
			logger.Info("[telegram] подключение установлено")
			<-ctx.Done()
			return ctx.Err()
		})
		c.mutex.Lock()
		c.api = nil
		c.mutex.Unlock()
		if ctx.Err() != nil {
			return
		}
		err = rpcError(err)
		delay := retryDelay(err)
		logger.Error("[telegram] ошибка подключения или авторизации", "error", err, "retry_after", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// retryDelay возвращает паузу перед повторной авторизацией.
func retryDelay(err error) time.Duration {
	delay := authRetryInterval
	var remote *Error
	if errors.As(err, &remote) && time.Duration(remote.RetryAfter)*time.Second > delay {
		delay = time.Duration(remote.RetryAfter) * time.Second
	}
	return delay
}

// WaitReady ожидает авторизованное подключение к Telegram.
func (c *Client) WaitReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ready:
		return nil
	}
}
