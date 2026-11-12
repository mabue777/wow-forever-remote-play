package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Game sound. sckcap writes Opus packets (20 ms each) to its fd 3 as [2-byte big-endian length][packet];
// they go out on their own track in their own stream ("wowtouch-audio"), so the browser does not hold the
// video back to line it up with the sound.

func newAudioTrack() *webrtc.TrackLocalStaticSample {
	t, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "wowtouch-audio")
	if err != nil {
		log.Fatal(err)
	}
	return t
}

func (h *hub) pumpAudio(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<16)
	var hdr [2]byte
	n := 0
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return
		}
		pkt := make([]byte, binary.BigEndian.Uint16(hdr[:]))
		if _, err := io.ReadFull(br, pkt); err != nil {
			return
		}
		h.audio.WriteSample(media.Sample{Data: pkt, Duration: 20 * time.Millisecond})
		if n++; n == 1 {
			log.Printf("audio: first packet (%d bytes)", len(pkt))
		}
	}
}
