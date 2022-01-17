package server

import (
	"context"
	"testing"

	"github.com/rfratto/viceroy/internal/fine"
	"github.com/stretchr/testify/require"
)

func TestChainMiddleware(t *testing.T) {
	var a, b, c, d int
	var called bool

	var mw = []Middleware{
		FuncMiddleware(func(ctx context.Context, h *fine.RequestHeader, req fine.Request, i Invoker) (fine.Response, error) {
			a = 10
			return i(ctx, h, req)
		}),
		FuncMiddleware(func(ctx context.Context, h *fine.RequestHeader, req fine.Request, i Invoker) (fine.Response, error) {
			b = 20
			return i(ctx, h, req)
		}),
		FuncMiddleware(func(ctx context.Context, h *fine.RequestHeader, req fine.Request, i Invoker) (fine.Response, error) {
			c = 30
			return i(ctx, h, req)
		}),
		FuncMiddleware(func(ctx context.Context, h *fine.RequestHeader, req fine.Request, i Invoker) (fine.Response, error) {
			d = 40
			return i(ctx, h, req)
		}),
	}

	invoker := func(context.Context, *fine.RequestHeader, fine.Request) (fine.Response, error) {
		called = true
		return nil, nil
	}
	chainMiddleware(mw).HandleRequest(context.Background(), nil, nil, invoker)

	require.Equal(t, 10, a)
	require.Equal(t, 20, b)
	require.Equal(t, 30, c)
	require.Equal(t, 40, d)
	require.True(t, called)
}

func TestChainMiddleware_Empty(t *testing.T) {
	var called bool

	invoker := func(context.Context, *fine.RequestHeader, fine.Request) (fine.Response, error) {
		called = true
		return nil, nil
	}

	chainMiddleware(nil).HandleRequest(context.Background(), nil, nil, invoker)
	require.True(t, called)
}
