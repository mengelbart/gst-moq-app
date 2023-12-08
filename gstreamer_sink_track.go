package main

import (
	"io"
	"log"

	"github.com/mengelbart/gst-go"
)

type gstreamerSinkTrack struct {
	p *gst.Pipeline
}

func newGstreamerSinkTrack() (*gstreamerSinkTrack, error) {
	p, err := gst.NewPipeline("appsrc name=src ! matroskademux ! decodebin ! videoconvert ! autovideosink")
	if err != nil {
		return nil, err
	}
	return &gstreamerSinkTrack{
		p: p,
	}, nil
}

func (t *gstreamerSinkTrack) start(r io.Reader) {
	t.p.SetEOSHandler(func() {
		t.p.Stop()
	})
	t.p.SetErrorHandler(func(err error) {
		log.Println(err)
		t.p.Stop()
	})
	t.p.Start()
	for {
		buf := make([]byte, 64_000)
		n, err := r.Read(buf)
		if err != nil {
			log.Printf("error on read: %v", err)
			t.p.SendEOS()
		}
		_, err = t.p.Write(buf[:n])
		if err != nil {
			log.Printf("error on write: %v", err)
			t.p.SendEOS()
		}
	}
}
