package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/mengelbart/moqtransport"
)

type sender struct {
	ctx       context.Context
	cancelCtx context.CancelFunc
}

func newSender() *sender {
	ctx, cancel := context.WithCancel(context.Background())
	return &sender{
		ctx:       ctx,
		cancelCtx: cancel,
	}
}

func (s *sender) Close() error {
	s.cancelCtx()
	gstDeinitOnce.Do(func() {
		gst.Deinit()
	})
	return nil
}

func (h *sender) HandleSubscription(session *moqtransport.Session, sub *moqtransport.Subscription, srw moqtransport.SubscriptionResponseWriter) {
	switch sub.Namespace {
	case "gstreamer":
		h.gstreamerSubscriptionHandler(session, sub, srw)
		return
	case "ffmpeg":
		h.ffmpegSubscriptionHandler(session, sub, srw)
		return
	}
	srw.Reject(0, "unknown namespace")
}

func (h *sender) gstreamerSubscriptionHandler(_ *moqtransport.Session, sub *moqtransport.Subscription, srw moqtransport.SubscriptionResponseWriter) {
	gstInitOnce.Do(func() {
		gst.Init(nil)
	})

	log.Printf("handling subscription to track %s/%s", sub.Namespace, sub.TrackName)
	if sub.TrackName != "video" {
		srw.Reject(0, errors.New("unknown trackname").Error())
		return
	}
	localTrack := moqtransport.NewLocalTrack(sub.TrackAlias, sub.Namespace, sub.TrackName)

	pipeline, err := gst.NewPipeline("")
	if err != nil {
		srw.Reject(0, "internal error")
		return
	}
	elements, err := gst.NewElementMany("videotestsrc", "queue", "videoconvert", "vp8enc")
	if err != nil {
		srw.Reject(0, "internal error")
		return
	}
	sink, err := app.NewAppSink()
	if err != nil {
		srw.Reject(0, "internal error")
		return
	}
	pipeline.AddMany(append(elements, sink.Element)...)
	gst.ElementLinkMany(append(elements, sink.Element)...)
	sink.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: func(sink *app.Sink) gst.FlowReturn {
			sample := sink.PullSample()
			if sample == nil {
				return gst.FlowEOS
			}
			buffer := sample.GetBuffer()
			if buffer == nil {
				return gst.FlowError
			}
			samples := buffer.Map(gst.MapRead).AsUint8Slice()
			defer buffer.Unmap()

			if err := localTrack.WriteObject(h.ctx, moqtransport.Object{
				GroupID:              0, // TODO
				ObjectID:             0, // TODO
				ObjectSendOrder:      0, // TODO
				ForwardingPreference: moqtransport.ObjectForwardingPreferenceStream,
				Payload:              samples,
			}); err != nil {
				return gst.FlowError
			}
			return gst.FlowOK
		},
	})
	if err := pipeline.SetState(gst.StatePlaying); err != nil {
		srw.Reject(0, "internal error")
		return
	}
	srw.Accept(localTrack)
	log.Printf("subscription accepted")
}

func (h *sender) ffmpegSubscriptionHandler(_ *moqtransport.Session, sub *moqtransport.Subscription, srw moqtransport.SubscriptionResponseWriter) {
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
		defer ffmpeg.Process.Kill()
		buf := make([]byte, 1024)
		n, err := reader.Read(buf)
		if err != nil {
			return
		}
		if err := localTrack.WriteObject(h.ctx, moqtransport.Object{
			GroupID:              groupID,
			ObjectID:             objectID,
			ObjectSendOrder:      0,
			ForwardingPreference: moqtransport.ObjectForwardingPreferenceStreamTrack,
			Payload:              buf[:n],
		}); err != nil {
			log.Println(err)
			return
		}
		objectID += 1
	}
}
