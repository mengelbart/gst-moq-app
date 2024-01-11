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
	"time"

	"github.com/mengelbart/gst-go"
	"github.com/mengelbart/moqtransport"
)

func main() {
	isServer := flag.Bool("server", false, "run as receiving server")
	flag.Parse()

	gst.GstInit()
	defer gst.GstDeinit()

	if *isServer {
		if err := server(context.TODO()); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := client(context.TODO()); err != nil {
		log.Fatal(err)
	}
}

func server(ctx context.Context) error {
	s := moqtransport.Server{
		Handler: moqtransport.SessionHandlerFunc(func(p *moqtransport.Session) {
			log.Println("handling new peer")
			defer log.Println("done")

			go func() {
				for {
					a, err := p.ReadAnnouncement(ctx)
					if err != nil {
						panic(err)
					}
					log.Printf("got announcement: %v", a)
				}
			}()
			if err := p.Announce(ctx, "video"); err != nil {
				log.Printf("failed to announce video: %v", err)
			}
			log.Println("announced video namespace")

			for {
				s, err := p.ReadSubscription(ctx)
				if err != nil {
					panic(err)
				}
				log.Printf("handling subscription to track %v", s)
				if s.Trackname() != "video" {
					panic(errors.New("unknown trackname"))
				}
				st := s.Accept()

				p, err := gst.NewPipeline("videotestsrc ! video/x-raw,format=I420,width=1280,height=720,framerate=30/1 ! vp8enc ! appsink name=appsink")
				if err != nil {
					log.Fatal(err)
				}
				p.SetBufferHandler(func(b gst.Buffer) {
					log.Printf("%v got buffer of size: %v and duration: %v", time.Now().UnixMilli(), len(b.Bytes), b.Duration)
					if _, err := st.Write(b.Bytes); err != nil {
						panic(err)
					}
				})
				p.SetEOSHandler(func() {
					p.Stop()
				})
				p.SetErrorHandler(func(err error) {
					log.Println(err)
					p.Stop()
				})
				p.Start()
			}
		}),
		TLSConfig: generateTLSConfig(),
	}
	if err := s.ListenQUIC(context.Background(), "localhost:1909"); err != nil {
		return err
	}
	return nil
}

func client(ctx context.Context) error {
	closeCh := make(chan struct{})

	c, err := moqtransport.DialQUIC("localhost:1909", moqtransport.IngestionDeliveryRole)
	if err != nil {
		return err
	}
	log.Println("moq peer connected")

	a, err := c.ReadAnnouncement(ctx)
	if err != nil {
		return err
	}
	a.Accept()

	log.Printf("got announcement: %v", a.Namespace())
	p, err := gst.NewPipeline("appsrc name=src ! video/x-vp8 ! vp8dec ! video/x-raw,format=I420,width=1280,height=720,framerate=30/1 ! autovideosink")
	if err != nil {
		return err
	}
	t, err := c.Subscribe(ctx, a.Namespace(), "video", "")
	if err != nil {
		return err
	}
	p.SetEOSHandler(func() {
		p.Stop()
		closeCh <- struct{}{}
	})
	p.SetErrorHandler(func(err error) {
		log.Println(err)
		p.Stop()
		closeCh <- struct{}{}
	})
	p.Start()
	log.Println("starting pipeline")
	go func() {
		for {
			log.Println("reading from track")
			buf := make([]byte, 10_000_000)
			n, err := t.Read(buf)
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

	ml := gst.NewMainLoop()
	go func() {
		<-closeCh
		ml.Stop()
	}()
	ml.Run()
	return nil
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
