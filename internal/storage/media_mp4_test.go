package storage

import (
	"encoding/binary"
	"os"
	"testing"
)

func mp4TestBox(kind string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box, uint32(len(box)))
	copy(box[4:], kind)
	copy(box[8:], payload)
	return box
}

func mp4TestTrack(handler string) []byte {
	payload := make([]byte, 24)
	copy(payload[8:], handler)
	return mp4TestBox("trak", mp4TestBox("mdia", mp4TestBox("hdlr", payload)))
}

func TestAudioMP4Validation(t *testing.T) {
	fixture, err := os.ReadFile("testdata/audio-only.mp4")
	if err != nil {
		t.Fatal(err)
	}
	ftyp := mp4TestBox("ftyp", []byte("isom\x00\x00\x00\x00mp42"))
	container := func(tracks []byte) []byte {
		result := append([]byte{}, ftyp...)
		result = append(result, mp4TestBox("moov", tracks)...)
		return append(result, mp4TestBox("mdat", []byte{1})...)
	}
	extended := make([]byte, 16)
	binary.BigEndian.PutUint32(extended, 1)
	copy(extended[4:], "free")
	binary.BigEndian.PutUint64(extended[8:], 16)
	oversized := append([]byte{}, extended...)
	binary.BigEndian.PutUint64(oversized[8:], ^uint64(0))
	endSized := container(mp4TestTrack("soun"))
	binary.BigEndian.PutUint32(endSized[len(endSized)-9:], 0) // mdat extends to EOF
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"real AAC-only MP4", fixture, true},
		{"audio track", container(mp4TestTrack("soun")), true},
		{"video track", container(mp4TestTrack("vide")), false},
		{"mixed tracks", container(append(mp4TestTrack("soun"), mp4TestTrack("vide")...)), false},
		{"no tracks", container(nil), false},
		{"unknown track", container(mp4TestTrack("meta")), false},
		{"missing handler", container(mp4TestBox("trak", mp4TestBox("mdia", nil))), false},
		{"extended box", append(extended, fixture...), true},
		{"oversized extended box", oversized, false},
		{"truncated extended box", extended[:12], false},
		{"zero size extends to EOF", endSized, true},
		{"box smaller than header", []byte{0, 0, 0, 4, 'f', 'r', 'e', 'e'}, false},
		{"forged type", []byte("\x00\x01\x02garbage"), false},
		{"truncated container", fixture[:len(fixture)-1], false},
		{"truncated header", []byte{0, 0, 0, 1}, false},
		{"oversized box", []byte{255, 255, 255, 255, 'f', 't', 'y', 'p'}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidatedMediaContentType("audio/mp4", tc.data, MediaKindAudio)
			if tc.valid && (err != nil || got != "audio/mp4") {
				t.Fatalf("audio rejected: %q %v", got, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("invalid audio accepted: %q", got)
			}
		})
	}
}
