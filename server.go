package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"

	"github.com/mengelbart/gst-go"
	"github.com/mengelbart/moqtransport"
	"github.com/mengelbart/moqtransport/quicmoq"
	"github.com/quic-go/quic-go"
)

type moqSenderSessionHandler struct {
	ctx         context.Context
	cancelCause context.CancelCauseFunc
}

type server struct {
	handler *moqSenderSessionHandler
	session *moqtransport.Session
}

func newServer(ctx context.Context, addr, certFile, keyFile string, gstreamer bool) (*server, error) {
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
	conn, err := listener.Accept(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)

	h := &moqSenderSessionHandler{
		ctx: ctx,
		cancelCause: func(cause error) {
			if gstreamer {
				gst.GstDeinit()
			}
			cancel(cause)
		},
	}
	sh := moqtransport.SubscriptionHandlerFunc(h.ffmpegSubscriptionHandler)
	if gstreamer {
		gst.GstInit()
		sh = moqtransport.SubscriptionHandlerFunc(h.gstreamerSubscriptionHandler)
	}
	return &server{
		handler: h,
		session: &moqtransport.Session{
			Conn:            quicmoq.New(conn),
			EnableDatagrams: true,
			LocalRole:       moqtransport.RolePublisher,
			RemoteRole:      0,
			AnnouncementHandler: moqtransport.AnnouncementHandlerFunc(func(s *moqtransport.Session, a *moqtransport.Announcement, arw moqtransport.AnnouncementResponseWriter) {
				log.Printf("got announcement: %v", a.Namespace())
				arw.Reject(0, "server does not accept announcements")
			}),
			SubscriptionHandler: sh,
		},
	}, nil
}

func (s *server) run(ctx context.Context) error {
	if err := s.session.RunServer(ctx); err != nil {
		return err
	}
	if err := s.session.Announce(ctx, "video"); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func (h *moqSenderSessionHandler) gstreamerSubscriptionHandler(_ *moqtransport.Session, sub *moqtransport.Subscription, srw moqtransport.SubscriptionResponseWriter) {
	log.Printf("handling subscription to track %s/%s", sub.Namespace, sub.TrackName)
	if sub.TrackName != "video" {
		err := errors.New("unknown trackname")
		srw.Reject(0, err.Error())
		return
	}
	localTrack := moqtransport.NewLocalTrack(sub.TrackAlias, sub.Namespace, sub.TrackName)
	p, err := gst.NewPipeline("videotestsrc is-live=true ! video/x-raw,width=640,height=360,framerate=30/1 ! clocksync ! vp8enc name=encoder cpu-used=16 deadline=1 keyframe-max-dist=100 ! appsink name=appsink")
	if err != nil {
		err = errors.New("internal error")
		srw.Reject(0, err.Error())
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
	p.SetBufferHandler(func(b gst.Buffer) {
		if err := localTrack.WriteObject(h.ctx, moqtransport.Object{
			GroupID:              0,
			ObjectID:             0,
			ObjectSendOrder:      0,
			ForwardingPreference: moqtransport.ObjectForwardingPreferenceStreamTrack,
			Payload:              b.Bytes,
		}); err != nil {
			h.cancelCause(err)
			return
		}
	})
	srw.Accept(localTrack)
	log.Printf("subscription accepted")
	p.Start()
}

func (h *moqSenderSessionHandler) ffmpegSubscriptionHandler(session *moqtransport.Session, sub *moqtransport.Subscription, srw moqtransport.SubscriptionResponseWriter) {
	defer h.cancelCause(nil)
	log.Printf("handling subscription to track %s/%s", sub.Namespace, sub.TrackName)
	if sub.TrackName != "video" {
		err := errors.New("unknown trackname")
		srw.Reject(0, err.Error())
		return
	}
	localTrack := moqtransport.NewLocalTrack(sub.TrackAlias, sub.Namespace, sub.TrackName)
	defer localTrack.Close()
	ffmpeg := exec.Command(
		"ffmpeg",
		"-hide_banner",
		"-v", "quiet",
		"-f", "lavfi",
		"-re",
		"-i", "testsrc",
		"-t", "3",
		"-f", "mp4", "-movflags", "cmaf+separate_moof+delay_moov+skip_trailer+frag_every_frame",
		"-",
	)
	ffmpeg.Stderr = os.Stderr
	reader, err := ffmpeg.StdoutPipe()
	if err != nil {
		panic(err)
	}
	if err := ffmpeg.Start(); err != nil {
		panic(err)
	}
	srw.Accept(localTrack)

	var (
		objectID uint64 = 0
		groupID  uint64 = 0
	)
	// TODO: This doesn't properly put cmaf into objects, it basically just
	// forwards the bitstream in arbitrarily sized chunks.
	for {
		buf := make([]byte, 1024)
		n, err := reader.Read(buf)
		if err != nil {
			return
		}
		localTrack.WriteObject(h.ctx, moqtransport.Object{
			GroupID:              groupID,
			ObjectID:             objectID,
			ObjectSendOrder:      0,
			ForwardingPreference: moqtransport.ObjectForwardingPreferenceStreamTrack,
			Payload:              buf[:n],
		})
		objectID += 1
	}
}
