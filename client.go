package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"os"
	"os/exec"

	"github.com/mengelbart/gst-go"
	"github.com/mengelbart/moqtransport"
	"github.com/mengelbart/moqtransport/quicmoq"
	"github.com/quic-go/quic-go"
)

type moqReceiverSessionHandler struct {
	ctx         context.Context
	cancelCause context.CancelCauseFunc
}

type client struct {
	handler *moqReceiverSessionHandler
	session *moqtransport.Session
}

func newClient(ctx context.Context, addr string, gstreamer bool) (*client, error) {
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
	ctx, cancel := context.WithCancelCause(ctx)
	h := &moqReceiverSessionHandler{
		ctx: ctx,
		cancelCause: func(cause error) {
			if gstreamer {
				gst.GstDeinit()
			}
			cancel(cause)
		},
	}
	ah := moqtransport.AnnouncementHandlerFunc(h.ffmpegAnnouncementHandler)
	if gstreamer {
		gst.GstInit()
		ah = moqtransport.AnnouncementHandlerFunc(h.gstreamerAnnouncementHandler)
	}
	return &client{
		handler: h,
		session: &moqtransport.Session{
			Conn:                quicmoq.New(conn),
			EnableDatagrams:     true,
			LocalRole:           moqtransport.RoleSubscriber,
			RemoteRole:          0,
			AnnouncementHandler: ah,
			SubscriptionHandler: nil,
		},
	}, nil
}

func (c *client) run(ctx context.Context) error {
	if err := c.session.RunClient(); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func (h *moqReceiverSessionHandler) gstreamerAnnouncementHandler(s *moqtransport.Session, a *moqtransport.Announcement, arw moqtransport.AnnouncementResponseWriter) {
	log.Printf("got announcement of namespace %v", a.Namespace())
	arw.Accept()
	sub, err := s.Subscribe(h.ctx, 0, 0, a.Namespace(), "video", "")
	if err != nil {
		return
	}
	log.Printf("subscribed to %v/%v", a.Namespace(), "video")
	p, err := gst.NewPipeline("appsrc name=src ! video/x-vp8 ! vp8dec ! video/x-raw,width=640,height=360,framerate=30/1 ! clocksync ! autovideosink")
	if err != nil {
		return
	}
	p.SetEOSHandler(func() {
		p.Stop()
		h.cancelCause(errors.New("EOS"))
	})
	p.SetErrorHandler(func(err error) {
		log.Println(err)
		p.Stop()
		h.cancelCause(err)
	})
	p.Start()

	go func() {
		for {
			log.Println("reading from track")
			obj, err := sub.ReadObject(h.ctx)
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
}

func (h *moqReceiverSessionHandler) ffmpegAnnouncementHandler(s *moqtransport.Session, a *moqtransport.Announcement, arw moqtransport.AnnouncementResponseWriter) {
	log.Printf("got announcement of namespace %v", a.Namespace())
	arw.Accept()
	sub, err := s.Subscribe(h.ctx, 0, 0, a.Namespace(), "video", "")
	if err != nil {
		return
	}
	log.Printf("subscribed to %v/%v", a.Namespace(), "video")

	ffplay := exec.Command(
		"ffplay",
		"-hide_banner",
		"-v", "quiet",
		"-",
	)
	ffplay.Stderr = os.Stderr
	writer, err := ffplay.StdinPipe()
	if err != nil {
		panic(err)
	}
	if err := ffplay.Start(); err != nil {
		panic(err)
	}
	for {
		obj, err := sub.ReadObject(h.ctx)
		if err != nil {
			panic(err)
		}
		if _, err := writer.Write(obj.Payload); err != nil {
			panic(err)
		}
	}
}
