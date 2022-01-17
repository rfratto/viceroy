package grpcfine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rfratto/viceroy/internal/fine"
	"github.com/vmihailenco/msgpack/v5"
	"google.golang.org/grpc/metadata"
)

type Codec interface {
	Name() string

	DecodeRequest(*Request) (fine.RequestHeader, fine.Request, error)
	DecodeResponse(*Response) (fine.ResponseHeader, fine.Response, error)
	EncodeRequest(*fine.RequestHeader, fine.Request) (*Request, error)
	EncodeResponse(*fine.ResponseHeader, fine.Response) (*Response, error)
}

// GetCodec retrieves a Codec from a gRPC request context. If there was no
// codec, the default codec is used.
func GetCodec(ctx context.Context) (Codec, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return MsgpackCodec(), nil
	}

	negotiated := md.Get("x-grpcfine-content-type")
	if len(negotiated) == 0 {
		return MsgpackCodec(), nil
	}
	for _, v := range negotiated {
		switch v {
		case "msgpack":
			return MsgpackCodec(), nil
		}
	}

	return nil, fmt.Errorf("no valid codecs within %q. supported codecs: msgpack", strings.Join(negotiated, ","))
}

// WithCodec injects a Condec into a gRPC reuqest context.
func WithCodec(ctx context.Context, c Codec) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	if md == nil {
		md = metadata.New(map[string]string{})
	}
	md.Append("x-grpcfine-content-type", c.Name())
	return metadata.NewIncomingContext(ctx, md)
}

// MsgpackCodec returns a Codec using msgpack.
func MsgpackCodec() Codec { return msgpackCodec{} }

type msgpackCodec struct{}

func (msgpackCodec) Name() string { return "msgpack" }

func (msgpackCodec) DecodeRequest(in *Request) (h fine.RequestHeader, r fine.Request, err error) {
	if err = msgpack.Unmarshal(in.GetHeader(), &h); err != nil {
		return
	}

	r, err = fine.NewEmptyRequest(h.Op)
	if errors.Is(err, fine.ErrorUnimplemented) {
		// ErrorUnimplemented means there's no request type for h.Op. We can
		// ignore.
		err = nil
	} else if err != nil {
		err = fmt.Errorf("couldn't get request for %s: %w", h.Op, err)
	} else {
		err = msgpack.Unmarshal(in.GetData(), r)
	}
	return
}

func (msgpackCodec) DecodeResponse(in *Response) (h fine.ResponseHeader, r fine.Response, err error) {
	if err = msgpack.Unmarshal(in.GetHeader(), &h); err != nil {
		return
	}

	r, err = fine.NewEmptyResponse(h.Op)
	if errors.Is(err, fine.ErrorUnimplemented) {
		// ErrorUnimplemented means there's no response type for h.Op. We can
		// ignore.
		err = nil
	} else if err != nil {
		err = fmt.Errorf("couldn't get response for %s: %w", h.Op, err)
	} else {
		err = msgpack.Unmarshal(in.GetData(), r)
	}
	return
}

func (msgpackCodec) EncodeRequest(h *fine.RequestHeader, r fine.Request) (res *Request, err error) {
	res = &Request{}
	res.Header, err = msgpack.Marshal(h)
	if err != nil {
		return nil, err
	}
	if r != nil {
		res.Data, err = msgpack.Marshal(r)
		if err != nil {
			return nil, err
		}
	}
	return
}

func (msgpackCodec) EncodeResponse(h *fine.ResponseHeader, r fine.Response) (res *Response, err error) {
	res = &Response{}
	res.Header, err = msgpack.Marshal(h)
	if err != nil {
		return nil, err
	}
	if r != nil {
		res.Data, err = msgpack.Marshal(r)
		if err != nil {
			return nil, err
		}
	}
	return
}
