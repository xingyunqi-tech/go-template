package outbound_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/outbound"
)

func TestRedactDropsTheQuery(t *testing.T) {
	t.Parallel()

	err := outbound.Redact(&url.Error{
		Op: "Get", URL: "https://api.weixin.qq.com/sns/jscode2session?appid=a&secret=s3cr3t", Err: context.DeadlineExceeded,
	})

	require.EqualError(t, err, "Get https://api.weixin.qq.com/sns/jscode2session: context deadline exceeded")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRedactKeepsOtherErrors(t *testing.T) {
	t.Parallel()

	plain := errors.New("plain")
	require.Equal(t, plain, outbound.Redact(plain))
}
