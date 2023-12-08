package main

import (
	"github.com/mengelbart/moqtransport"
	"github.com/quic-go/quic-go/quicvarint"
)

type feedbackTrack struct {
	bitrateCh chan uint64
}

func newFeedbackTrackFactory(bitrateCh chan uint64) func() (srcTrack, error) {
	return func() (srcTrack, error) {
		return &feedbackTrack{
			bitrateCh: bitrateCh,
		}, nil
	}
}

func (t *feedbackTrack) start(track *moqtransport.SendTrack) {
	for bitrate := range t.bitrateCh {
		b := make([]byte, 0, quicvarint.Len(bitrate))
		if _, err1 := track.Write(quicvarint.Append(b, bitrate)); err1 != nil {
			panic(err1)
		}
	}
}

func (s *feedbackTrack) setBitrate(bps uint64) {
	// don't care for now
}
