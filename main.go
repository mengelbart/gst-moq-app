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
	"os"
	"os/signal"
	"syscall"

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
	gstreamer := flag.Bool("gst", false, "use Gstreamer instead of FFMPEG")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigs:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	if *isServer {
		s, err := newServer(ctx, *addr, *cert, *key, *gstreamer)
		if err != nil {
			log.Fatal(err)
		}
		if err := s.run(ctx); err != nil {
			log.Fatal(err)
		}
		return
	}
	c, err := newClient(ctx, *addr, *gstreamer)
	if err != nil {
		log.Fatal(err)
	}
	if err := c.run(ctx); err != nil {
		log.Fatal(err)
	}
}

func client1(ctx context.Context, addr string) error {
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
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	s := moqtransport.Session{
		Conn:            quicmoq.New(conn),
		EnableDatagrams: true,
		LocalRole:       moqtransport.RoleSubscriber,
		RemoteRole:      0,
		AnnouncementHandler: moqtransport.AnnouncementHandlerFunc(func(s *moqtransport.Session, a *moqtransport.Announcement, arw moqtransport.AnnouncementResponseWriter) {
			log.Printf("got announcement of namespace %v", a.Namespace())
			arw.Accept()
			sub, err := s.Subscribe(ctx, 0, 0, a.Namespace(), "video", "")
			if err != nil {
				return
			}
			log.Printf("subscribed to %v/%v", a.Namespace(), "video")
			// p, err := gst.NewPipeline("appsrc name=src ! video/x-vp8 ! vp8dec ! video/x-raw, format=(string)I420, width=(int)1280, height=(int)720 ! autovideosink")
			p, err := gst.NewPipeline("appsrc name=src ! video/x-vp8 ! vp8dec ! video/x-raw,width=1280,height=720,framerate=30/1 ! clocksync ! autovideosink")

			// 			p, err := gst.NewPipeline("appsrc name=src ! video/x-vp8, profile=(string)0, streamheader=(buffer)< 4f56503830010100050002d00000010000010000003c00000001 >, width=(int)1280, height=(int)720, pixel-aspect-ratio=(fraction)1/1, framerate=(fraction)60/1, interlace-mode=(string)progressive, colorimetry=(string)bt709, chroma-site=(string)mpeg2, multiview-mode=(string)mono, multiview-flags=(GstVideoMultiviewFlagsSet)0:ffffffff:/right-view-first/left-flipped/left-flopped/right-flipped/right-flopped/half-aspect/mixed-mono ! vp8dec ! video/x-raw, format=(string)I420, width=(int)1280, height=(int)720 ! autovideosink")
			if err != nil {
				return
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
					obj, err := sub.ReadObject(ctx)
					if err != nil {
						log.Printf("error on read: %v", err)
						p.SendEOS()
					}
					log.Printf("writing %v bytes from stream to pipeline", len(obj.Payload))
					_, err = p.Write(obj.Payload)
					if err != nil {
						log.Printf("error on write: %v", err)
						p.SendEOS()
					}
				}
			}()
		}),
		SubscriptionHandler: nil,
	}

	if err := s.RunClient(); err != nil {
		return err
	}
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
