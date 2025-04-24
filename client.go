package main

import (
	"context"
	"crypto/tls"
	"sync"

	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/moqtransport"
	"github.com/mengelbart/moqtransport/quicmoq"
	"github.com/quic-go/quic-go"
)

type client struct {
	ctx           context.Context
	cancelCtx     context.CancelFunc
	session       *moqtransport.Session
	transport     *moqtransport.Transport
	gstInitOnce   sync.Once
	gstDeInitOnce sync.Once
}

func newClient(ctx context.Context, addr string, sender *sender) (*client, error) {
	conn, err := quic.DialAddr(ctx, addr, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"moq-00"},
	}, &quic.Config{
		EnableDatagrams:            true,
		MaxIncomingStreams:         1 << 60,
		MaxStreamReceiveWindow:     1 << 60,
		MaxIncomingUniStreams:      1 << 60,
		MaxConnectionReceiveWindow: 1 << 60,
	})
	if err != nil {
		return nil, err
	}
	moqConn := quicmoq.NewClient(conn)
	session := moqtransport.NewSession(moqConn.Protocol(), moqConn.Perspective(), 0)
	transport := &moqtransport.Transport{
		Conn:    moqConn,
		Handler: sender,
		Qlogger: nil,
		Session: session,
	}
	if err := transport.Run(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &client{
		ctx:           ctx,
		cancelCtx:     cancel,
		session:       session,
		transport:     transport,
		gstInitOnce:   sync.Once{},
		gstDeInitOnce: sync.Once{},
	}, nil
}

func (c *client) Close() error {
	c.cancelCtx()
	c.gstInitOnce.Do(func() {
		gst.Deinit()
	})
	return nil
}

func (c *client) run(ctx context.Context, gstreamer bool, namespace string) error {
	if len(namespace) > 0 {
		r := newReceiver(c.session)
		defer r.Close()
		if err := r.subscribe(gstreamer, namespace); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}
