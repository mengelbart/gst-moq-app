package main

import (
	"context"
	"log"

	"github.com/mengelbart/moqtransport"
	"github.com/mengelbart/moqtransport/quicmoq"
	"github.com/quic-go/quic-go"
)

type server struct {
	session *moqtransport.Session
}

func newServer(ctx context.Context, addr, certFile, keyFile string, sender *sender) (*server, error) {
	tlsConfig, err := generateTLSConfigWithCertAndKey(certFile, keyFile)
	if err != nil {
		log.Printf("failed to generate TLS config from cert file and key, generating in memory certs: %v", err)
		tlsConfig = generateTLSConfig()
	}
	listener, err := quic.ListenAddr(addr, tlsConfig, &quic.Config{
		EnableDatagrams:            true,
		MaxIncomingStreams:         1 << 60,
		MaxStreamReceiveWindow:     1 << 60,
		MaxIncomingUniStreams:      1 << 60,
		MaxConnectionReceiveWindow: 1 << 60,
	})
	if err != nil {
		return nil, err
	}
	// TODO: Accept in loop
	conn, err := listener.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return &server{
		session: &moqtransport.Session{
			Conn:                quicmoq.New(conn),
			EnableDatagrams:     true,
			LocalRole:           moqtransport.RolePubSub,
			RemoteRole:          0,
			AnnouncementHandler: nil,
			SubscriptionHandler: sender,
		},
	}, nil
}

func (s *server) run(ctx context.Context, gstreamer bool, namespace string) error {
	if err := s.session.RunServer(ctx); err != nil {
		return err
	}
	if len(namespace) > 0 {
		r := newReceiver(s.session)
		defer r.Close()
		if err := r.subscribe(gstreamer, namespace); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}
