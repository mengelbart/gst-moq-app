package main

import (
	"log"

	"github.com/mengelbart/gst-go"
	"github.com/mengelbart/moqtransport"
)

type gstreamerSrcTrack struct {
	p *gst.Pipeline
}

func newGstreamerSrcTrack() (srcTrack, error) {
	p, err := gst.NewPipeline("videotestsrc ! video/x-raw,width=1280,height=720 ! clocksync ! x264enc name=encoder pass=5 speed-preset=4 tune=4 bitrate=1000 ! matroskamux ! appsink name=appsink")
	if err != nil {
		return nil, err
	}
	return &gstreamerSrcTrack{
		p: p,
	}, nil
}

func (t *gstreamerSrcTrack) start(track *moqtransport.SendTrack) {
	t.p.SetBufferHandler(func(b gst.Buffer) {
		if _, err := track.Write(b.Bytes); err != nil {
			panic(err)
		}
	})
	t.p.SetEOSHandler(func() {
		t.p.Stop()
	})
	t.p.SetErrorHandler(func(err error) {
		log.Println(err)
		t.p.Stop()
	})
	t.p.Start()
}

func (s *gstreamerSrcTrack) setBitrate(bps uint64) {
	log.Printf("update bitrate from %v to: %v", s.p.GetPropertyUint("encoder", "bitrate"), bps/1000)
	s.p.SetPropertyUint("encoder", "bitrate", uint(bps/1000))
	log.Printf("new bitrate is: %v", s.p.GetPropertyUint("encoder", "bitrate"))
}
