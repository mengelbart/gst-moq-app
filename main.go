package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"log"
	"math/big"
	"time"

	"github.com/mengelbart/gst-go"
	"github.com/mengelbart/moqtransport"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/logging"
)

const alpn = "moq-01"

func main() {
	isServer := flag.Bool("server", false, "run as receiving server")
	flag.Parse()

	gst.GstInit()
	defer gst.GstDeinit()

	if *isServer {
		if err := server(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := client(); err != nil {
		log.Fatal(err)
	}
}

func server() error {
	ph := newPeerHandler([]string{"moq"})
	ph.addMediaTrackFactory("moq/video", newGstreamerSrcTrack)
	s := moqtransport.Server{
		Handler:   ph,
		TLSConfig: generateTLSConfig(),
	}
	if err := s.ListenQUIC(context.Background(), "localhost:1909"); err != nil {
		return err
	}
	return nil
}

func client() error {
	tlsConf := tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{alpn},
	}

	bitrateCh := make(chan uint64, 100)
	conn, err := quic.DialAddr(context.TODO(), "localhost:1909", &tlsConf, &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		EnableDatagrams: true,
		Tracer: func(_ context.Context, _ logging.Perspective, _ quic.ConnectionID) *logging.ConnectionTracer {
			lastEntry := time.Now()
			sumReceived := 0
			return &logging.ConnectionTracer{
				ReceivedShortHeaderPacket: func(_ *logging.ShortHeader, bc logging.ByteCount, _ logging.ECN, _ []logging.Frame) {
					sumReceived += int(bc)
					if time.Since(lastEntry) > time.Second {
						bits := 8 * sumReceived
						bitrateCh <- uint64(bits)
						log.Printf("received %v bytes (%v bits) in last second => %v Mbit/s", sumReceived, bits, float64(bits)/1e6)
						lastEntry = time.Now()
						sumReceived = 0
					}
				},
			}
		},
	})
	if err != nil {
		return err
	}
	c, err := moqtransport.DialQUICConn(conn, moqtransport.DeliveryRole)
	if err != nil {
		return err
	}
	log.Println("moq peer connected")
	ph := newPeerHandler([]string{"moqfb"})
	ph.addMediaTrackFactory("moqfb/rate", newFeedbackTrackFactory(bitrateCh))
	ph.Handle(c)
	select {}
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
		NextProtos:   []string{alpn},
	}
}
