package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/mengelbart/moqtransport"
	"github.com/quic-go/quic-go/quicvarint"
)

type srcTrack interface {
	start(*moqtransport.SendTrack)
	setBitrate(uint64)
}

type mediaTrackFactory func() (srcTrack, error)

type trackKey struct {
	fullTrackname string
	id            uint64
}

type peerHandler struct {
	p                   *moqtransport.Peer
	mediaTrackFactories map[string]mediaTrackFactory
	mediaTracks         map[trackKey]srcTrack
	nextID              int
	namespaces          []string
}

func newPeerHandler(namespaces []string) *peerHandler {
	return &peerHandler{
		p:                   nil,
		mediaTrackFactories: map[string]mediaTrackFactory{},
		mediaTracks:         map[trackKey]srcTrack{},
		nextID:              0,
		namespaces:          namespaces,
	}
}

func (p *peerHandler) addMediaTrackFactory(name string, mf mediaTrackFactory) {
	p.mediaTrackFactories[name] = mf
}

func (h *peerHandler) Handle(p *moqtransport.Peer) {
	log.Println("handling new peer")
	h.p = p

	go func() {
		for {
			a, err := h.p.ReadAnnouncement(context.Background())
			if err != nil {
				panic(err)
			}
			log.Printf("got announcement: %v", a.Namespace())
			switch a.Namespace() {
			case "moqfb":
				a.Accept()
				h.setupReceiverEstimatedBitrateSubscription()
			case "moq":
				a.Accept()
				h.setupMediaSubscription(a.Namespace())
			default:
				a.Reject(errors.New("unknown namespace"))
			}
		}
	}()

	go func() {
		for {
			s, err := h.p.ReadSubscription(context.Background())
			if err != nil {
				panic(err)
			}
			fulltrackname := fmt.Sprintf("%v/%v", s.Namespace(), s.Trackname())
			if mtf, ok := h.mediaTrackFactories[fulltrackname]; ok {
				s.SetTrackID(uint64(h.nextID))
				h.nextID++
				mt, err := mtf()
				if err != nil {
					s.Reject(err)
					continue
				}
				h.mediaTracks[trackKey{
					fullTrackname: fulltrackname,
					id:            s.TrackID(),
				}] = mt
				track := s.Accept()
				mt.start(track)
				continue
			}
			s.Reject(errors.New("unknown trackname"))
		}
	}()

	for _, ns := range h.namespaces {
		if err := h.p.Announce(ns); err != nil {
			log.Printf("failed to announce ns %v: %v", ns, err)
		}
		log.Printf("announced namespace: %v", ns)
	}
}

func (h *peerHandler) setupReceiverEstimatedBitrateSubscription() {
	rt, err := h.p.Subscribe("moqfb", "rate", "")
	if err != nil {
		panic(err)
	}
	lastProbingEnd := time.Time{}
	probingStart := time.Time{}
	probing := false
	go func() {
		buf := make([]byte, 1500)
		bitrate1mbps := 1_000_000
		bitrate3mbps := 3_000_000
		bitrate5mbps := 5_000_000
		currentBitrate := bitrate1mbps
		for {
			n, err := rt.Read(buf)
			if err != nil {
				panic(err)
			}
			val, err := quicvarint.Read(bytes.NewReader(buf[:n]))
			if err != nil {
				panic(err)
			}
			log.Printf("got bitrate fb: %v", val)
			if probing && time.Since(probingStart) < 5*time.Second {
				log.Println("probing")
				continue
			}
			if probing && time.Since(probingStart) > 5*time.Second {
				log.Printf("stop probing after %v", time.Since(probingStart))
				probing = false
				lastProbingEnd = time.Now()
				continue
			}
			if time.Since(lastProbingEnd) > 10*time.Second {
				log.Printf("start probing after %v", time.Since(lastProbingEnd))
				probing = true
				probingStart = time.Now()
				h.updateBitrateShares(uint64(3 * currentBitrate))
				currentBitrate = 3 * currentBitrate
				continue
			}
			switch {
			case float64(val) < 0.9*float64(bitrate3mbps):
				h.updateBitrateShares(uint64(bitrate1mbps))
				currentBitrate = bitrate1mbps
			case float64(val) < 0.9*float64(bitrate5mbps):
				h.updateBitrateShares(uint64(bitrate3mbps))
				currentBitrate = bitrate3mbps
			default:
				h.updateBitrateShares(uint64(bitrate5mbps))
				currentBitrate = bitrate5mbps
			}
		}
	}()
}

func (h *peerHandler) updateBitrateShares(bps uint64) {
	numTracks := len(h.mediaTracks)
	share := bps / uint64(numTracks)
	for _, t := range h.mediaTracks {
		t.setBitrate(share)
	}
}

func (h *peerHandler) setupMediaSubscription(namespace string) {
	r, err := h.p.Subscribe(namespace, "video", "")
	if err != nil {
		log.Println(err)
		return
	}
	t, err := newGstreamerSinkTrack()
	if err != nil {
		log.Println(err)
		return
	}
	t.start(r)
}
