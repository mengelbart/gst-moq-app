package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"io"
	"log"
	"log/slog"
	"math/big"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mengelbart/moqtransport"
	"github.com/quic-go/quic-go/quicvarint"
)

var gstInitOnce sync.Once
var gstDeinitOnce sync.Once

func main() {
	moqtransport.SetLogHandler(slog.NewJSONHandler(
		io.Discard,
		&slog.HandlerOptions{
			AddSource: false,
			Level:     nil,
		},
	))

	isServer := flag.Bool("server", false, "true: run as sending server, false: run as receiving client")
	cert := flag.String("cert", "localhost.pem", "TLS certificate file (server only)")
	key := flag.String("key", "localhost-key.pem", "TLS key file (server only)")
	addr := flag.String("addr", "localhost:8080", "server address")
	gstreamer := flag.Bool("gst", false, "use Gstreamer instead of FFMPEG")
	namespace := flag.String("sub", "", "subscribe to track 'video' in <namespace>")
	feedback := flag.Bool("fb", false, "subscribe to feedback track")
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

	sender := newSender()

	// TODO: Replace this goroutine with correct feedback generator
	// (this just updates the bitrate every second)
	go func() {
		ticker := time.NewTicker(time.Second)
		i := 0
		for {
			select {
			case <-ticker.C:
				newRate := ((i % 20) + 1) * 10000
				log.Printf("sending rate: %v", newRate)
				if err := sender.sendFeedback(quicvarint.Append([]byte{}, uint64(newRate))); err != nil {
					panic(err)
				}
				i++
			case <-ctx.Done():
				return
			}
		}
	}()
	if *isServer {
		s, err := newServer(ctx, *addr, *cert, *key, sender)
		if err != nil {
			log.Fatal(err)
		}
		if err := s.run(ctx, *gstreamer, *feedback, *namespace); err != nil {
			log.Fatal(err)
		}
		return
	}
	c, err := newClient(ctx, *addr, sender)
	if err != nil {
		log.Fatal(err)
	}
	if err := c.run(ctx, *gstreamer, *feedback, *namespace); err != nil {
		log.Fatal(err)
	}
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
