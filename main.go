package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"log"
	"math/big"

	"github.com/mengelbart/gst-go"
	"github.com/mengelbart/moqtransport"
	"github.com/mengelbart/moqtransport/quicmoq"
	"github.com/quic-go/quic-go"
)

func main() {
	isServer := flag.Bool("server", false, "true: run as sending server, false: run as receiving client")
	cert := flag.String("cert", "localhost.pem", "TLS certificate file (server only)")
	key := flag.String("key", "localhost-key.pem", "TLS key file (server only)")
	addr := flag.String("addr", "localhost:8080", "server address")
	flag.Parse()

	gst.GstInit()
	defer gst.GstDeinit()

	ctx := context.Background()

	if *isServer {
		if err := server(ctx, *addr, *cert, *key); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := client(ctx, *addr); err != nil {
		log.Fatal(err)
	}
}

func server(ctx context.Context, addr, certFile, keyFile string) error {
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
		return err
	}
	conn, err := listener.Accept(ctx)
	if err != nil {
		return err
	}
	s, err := moqtransport.NewServerSession(quicmoq.New(conn), true)
	if err != nil {
		return err
	}

	log.Printf("handling new peer")
	go func() {
		var a *moqtransport.Announcement
		a, err = s.ReadAnnouncement(ctx)
		if err != nil {
			return
		}
		log.Printf("got announcement: %v", a.Namespace())
		a.Reject(0, "server does not accept announcements")
	}()

	go func() {
		if err = s.Announce(ctx, "video"); err != nil {
			return
		}
		log.Printf("announced video namespace")
	}()

	sub, err := s.ReadSubscription(ctx)
	if err != nil {
		return err
	}
	log.Printf("handling subscription to track %s/%s", sub.Namespace(), sub.Trackname())
	if sub.Trackname() != "video" {
		err = errors.New("unknown trackname")
		sub.Reject(0, err.Error())
		return err
	}
	sub.Accept()
	log.Printf("subscription accepted")

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	p, err := gst.NewPipeline("videotestsrc is-live=true ! video/x-raw,width=480,height=320,framerate=30/1 ! clocksync ! vp8enc name=encoder target-bitrate=10000000 cpu-used=16 deadline=1 keyframe-max-dist=10 ! appsink name=appsink")
	if err != nil {
		err = errors.New("internal error")
		sub.Reject(0, err.Error())
		return err
	}
	p.SetEOSHandler(func() {
		p.Stop()
		cancel(errors.New("EOS"))
	})
	p.SetErrorHandler(func(err error) {
		log.Println(err)
		p.Stop()
		cancel(err)
	})
	p.SetBufferHandler(func(b gst.Buffer) {
		stream, err := sub.NewObjectStream(0, 0, 0)
		if err != nil {
			cancel(err)
			return
		}
		if _, err := stream.Write(b.Bytes); err != nil {
			cancel(err)
			return
		}
		if err := stream.Close(); err != nil {
			cancel(err)
			return
		}
	})
	p.Start()

	<-ctx.Done()

	return context.Cause(ctx)
}

func client(ctx context.Context, addr string) error {
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
		return err
	}
	s, err := moqtransport.NewClientSession(quicmoq.New(conn), moqtransport.DeliveryRole, true)
	if err != nil {
		return err
	}
	a, err := s.ReadAnnouncement(ctx)
	if err != nil {
		return err
	}
	log.Printf("got announcement of namespace %v", a.Namespace())
	a.Accept()
	sub, err := s.Subscribe(ctx, 0, 0, a.Namespace(), "video", "")
	if err != nil {
		return err
	}
	log.Printf("subscribed to %v/%v", a.Namespace(), "video")
	ctx, cancel := context.WithCancelCause(ctx)
	p, err := gst.NewPipeline("appsrc name=src ! video/x-vp8 ! vp8dec ! video/x-raw,width=480,height=320,framerate=30/1 ! autovideosink")
	if err != nil {
		return err
	}
	p.SetEOSHandler(func() {
		p.Stop()
		cancel(errors.New("EOS"))
	})
	p.SetErrorHandler(func(err error) {
		log.Println(err)
		p.Stop()
		cancel(err)
	})
	p.Start()

	go func() {
		for {
			log.Println("reading from track")
			buf := make([]byte, 64_000)
			n, err := sub.Read(buf)
			if err != nil {
				log.Printf("error on read: %v", err)
				p.SendEOS()
			}
			log.Printf("writing %v bytes from stream to pipeline", n)
			_, err = p.Write(buf[:n])
			if err != nil {
				log.Printf("error on write: %v", err)
				p.SendEOS()
			}
		}
	}()

	<-ctx.Done()

	return context.Cause(ctx)
}

// Setup a bare-bones TLS config for the server
func generateTLSConfig() *tls.Config {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"moq-00"},
	}
}

func generateTLSConfigWithCertAndKey(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"moq-00"},
	}, nil
}
