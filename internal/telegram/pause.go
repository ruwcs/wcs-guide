package telegram

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/gotd/td/bin"
	td "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// rateLimit ограничивает запросы Telegram на время обязательной паузы.
func (c *Client) rateLimit(next tg.Invoker) td.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mutex.RLock()
		until := c.pausedUntil
		c.mutex.RUnlock()
		if remaining := time.Until(until); remaining > 0 {
			return &Error{Code: "rate_limit", RetryAfter: int(math.Ceil(remaining.Seconds()))}
		}
		err := next.Invoke(ctx, input, output)
		var remote *Error
		if errors.As(rpcError(err), &remote) && remote.Code == "rate_limit" && remote.RetryAfter > 0 {
			c.mutex.Lock()
			until := time.Now().Add(time.Duration(remote.RetryAfter) * time.Second)
			if until.After(c.pausedUntil) {
				c.pausedUntil = until
			}
			c.mutex.Unlock()
		}
		return err
	}
}
