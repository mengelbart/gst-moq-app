package main

import (
	"context"
	"os"
	"os/exec"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/mengelbart/moqtransport"
)

type receiver struct {
	ctx       context.Context
	cancelCtx context.CancelFunc
	session   *moqtransport.Session
}

func newReceiver(s *moqtransport.Session) *receiver {
	ctx, cancel := context.WithCancel(context.Background())
	return &receiver{
		ctx:       ctx,
		cancelCtx: cancel,
		session:   s,
	}
}

func (r *receiver) Close() error {
	r.cancelCtx()
	gstDeinitOnce.Do(func() {
		gst.Deinit()
	})
	return nil
}

func (r *receiver) subscribe(gstreamer bool, namespace string) error {
	track, err := r.session.Subscribe(r.ctx, 0, 0, namespace, "video", "")
	if err != nil {
		return err
	}
	if gstreamer {
		return r.receiveGstreamer(track)
	} else {
		return r.receiveFFmpeg(track)
	}
}

func (r *receiver) receiveGstreamer(track *moqtransport.RemoteTrack) error {
	gstInitOnce.Do(func() {
		gst.Init(nil)
	})
	pipeline, err := gst.NewPipeline("")
	if err != nil {
		return err
	}
	elements, err := gst.NewElementMany("vp8dec", "autovideosink")
	if err != nil {
		return err
	}
	src, err := app.NewAppSrc()
	if err != nil {
		return err
	}
	src.SetCaps(gst.NewCapsFromString("video/x-vp8"))
	ee := append([]*gst.Element{src.Element}, elements...)
	if err := pipeline.AddMany(ee...); err != nil {
		return err
	}
	if err := gst.ElementLinkMany(ee...); err != nil {
		return err
	}
	src.SetCallbacks(&app.SourceCallbacks{
		NeedDataFunc: func(src *app.Source, length uint) {
			o, err := track.ReadObject(r.ctx)
			if err != nil {
				src.EndStream()
				return
			}
			buffer := gst.NewBufferWithSize(int64(len(o.Payload)))
			buffer.Map(gst.MapWrite).WriteData(o.Payload)
			defer buffer.Unmap()
			src.PushBuffer(buffer)
		},
	})
	go func() {
		<-r.ctx.Done()
		pipeline.SetState(gst.StateNull)
	}()
	return pipeline.SetState(gst.StatePlaying)
}

func (r *receiver) receiveFFmpeg(track *moqtransport.RemoteTrack) error {
	ffplay := exec.Command(
		"ffplay",
		"-hide_banner",
		// "-v", "quiet",
		"-",
	)
	ffplay.Stderr = os.Stderr
	writer, err := ffplay.StdinPipe()
	if err != nil {
		return err
	}
	if err := ffplay.Start(); err != nil {
		return err
	}
	go func() {
		defer ffplay.Process.Kill()
		for {
			obj, err := track.ReadObject(r.ctx)
			if err != nil {
				return
			}
			if _, err := writer.Write(obj.Payload); err != nil {
				return
			}
		}
	}()
	return nil
}
