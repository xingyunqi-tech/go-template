package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// Pinger sends PING to Redis.
type Pinger struct {
	client *goredis.Client
}

// NewPinger returns a Pinger for client.
func NewPinger(client *goredis.Client) *Pinger {
	return &Pinger{client: client}
}

var _ repo.Pinger = (*Pinger)(nil)

// Ping fails when Redis does not answer PONG.
func (p *Pinger) Ping(ctx context.Context) error {
	if err := p.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: ping: %w", err)
	}

	return nil
}
