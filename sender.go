package main

import (
	"context"
	"errors"
	"fmt"
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

func (h *sender) Handle(w moqtransport.ResponseWriter, m *moqtransport.Message) {
	if m.Method != moqtransport.MessageSubscribe {
		return
	}
	if len(m.Namespace) != 1 {
		w.Reject(moqtransport.ErrorCodeSubscribeTrackDoesNotExist, "unknown track")
		return
	}
	switch m.Namespace[0] {
	case "gstreamer":
		if err := h.gstreamerSubscriptionHandler(m.Namespace[0], m.Track, w.(moqtransport.Publisher)); err != nil {
			w.Reject(moqtransport.ErrorCodeSubscribeInternal, fmt.Sprintf("failed to setup gstreamer pipeline: %v", err))
			return
		}
		w.Accept()
	case "ffmpeg":
		if err := h.ffmpegSubscriptionHandler(m.Namespace[0], m.Track, w.(moqtransport.Publisher)); err != nil {
			w.Reject(moqtransport.ErrorCodeSubscribeInternal, fmt.Sprintf("failed to setup ffmpeg pipeline: %v", err))
			return
		}
		w.Accept()
	default:
		w.Reject(moqtransport.ErrorCodeSubscribeTrackDoesNotExist, "unknown track")
	}
}

func (h *sender) gstreamerSubscriptionHandler(namespace, trackname string, publisher moqtransport.Publisher) error {
	gstInitOnce.Do(func() {
		gst.Init(nil)
	})

	log.Printf("handling subscription to track %s/%s", namespace, trackname)
	if trackname != "video" {
		return errors.New("unknown track")
	}

	pipeline, err := gst.NewPipeline("")
	if err != nil {
		return err
	}
	elements, err := gst.NewElementMany("videotestsrc", "queue", "videoconvert", "vp8enc")
	if err != nil {
		return err
	}
	sink, err := app.NewAppSink()
	if err != nil {
		return err
	}

	var (
		objectID uint64 = 0
		groupID  uint64 = 0
	)

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

			sg, err := publisher.OpenSubgroup(groupID, 0, 0)
			if err != nil {
				log.Printf("failed to open subgroup: %v", err)
				return gst.FlowError
			}
			_, err = sg.WriteObject(objectID, samples)
			if err != nil {
				log.Printf("failed to write object to subgroup: %v", err)
				return gst.FlowError
			}
			sg.Close()
			groupID++
			objectID++
			return gst.FlowOK
		},
	})
	return pipeline.SetState(gst.StatePlaying)
}

func (h *sender) ffmpegSubscriptionHandler(namespace, trackname string, publisher moqtransport.Publisher) error {
	log.Printf("handling subscription to track %s/%s", namespace, trackname)
	if trackname != "video" {
		return errors.New("unknown trackname")
	}
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
		return err
	}
	if err := ffmpeg.Start(); err != nil {
		return err
	}

	var (
		objectID uint64 = 0
		groupID  uint64 = 0
	)
	// TODO: This doesn't properly put cmaf into objects, it basically just
	// forwards the bitstream in arbitrarily sized chunks.
	go func() {
		for {
			defer ffmpeg.Process.Kill()
			buf := make([]byte, 1024)
			n, err := reader.Read(buf)
			if err != nil {
				return
			}
			sg, err := publisher.OpenSubgroup(groupID, 0, 0)
			if err != nil {
				log.Printf("failed to open subgroup: %v", err)
				return
			}
			_, err = sg.WriteObject(objectID, buf[:n])
			if err != nil {
				log.Printf("failed to write object to subgroup: %v", err)
				return
			}
			sg.Close()
			groupID++
			objectID++
		}
	}()
	return nil
}
