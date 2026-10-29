package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"testing"
)

// power returns the energy of pcm at freq Hz (Goertzel), normalised by length.
func power(pcm []int16, freq float64) float64 {
	k := 2 * math.Cos(2*math.Pi*freq/SampleRate)
	var s1, s2 float64
	for _, v := range pcm {
		s1, s2 = float64(v)+k*s1-s2, s1
	}
	return (s1*s1 + s2*s2 - k*s1*s2) / float64(len(pcm)*len(pcm))
}

func TestDecodeTone(t *testing.T) {
	for _, name := range []string{"tone-voip16k.ogg", "tone-audio48k.ogg"} {
		t.Run(name, func(t *testing.T) {
			pcm, err := DecodeFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			// Pre-skip and end trimming leave exactly the 3 s that were encoded.
			if len(pcm) != 3*SampleRate {
				t.Fatalf("got %d samples, want %d", len(pcm), 3*SampleRate)
			}
			tone, off := power(pcm, 440), power(pcm, 1000)
			if tone < 1000*off {
				t.Fatalf("440 Hz is not dominant: %g vs %g at 1 kHz", tone, off)
			}
			// The tone was encoded at 0.4 full scale; the codec keeps the level.
			var peak int16
			for _, v := range pcm[SampleRate : 2*SampleRate] {
				peak = max(peak, v)
			}
			if peak < 11000 || peak > 15000 {
				t.Fatalf("peak %d, want about %d", peak, 13107)
			}
		})
	}
}

func TestDecodeSpeech(t *testing.T) {
	pcm, err := DecodeFile("testdata/jfk-voip16k.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 176000 {
		t.Fatalf("got %d samples, want 176000 (11 s)", len(pcm))
	}
}

func TestDecodeTruncatedKeepsAudio(t *testing.T) {
	data, err := os.ReadFile("testdata/jfk-voip16k.ogg")
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := Decode(bytes.NewReader(data[:len(data)/2]))
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) < 3*SampleRate || len(pcm) >= 176000 {
		t.Fatalf("got %d samples from half a file", len(pcm))
	}
}

func TestDecodeRejectsOtherFormats(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty": nil,
		"wav":   WAV(make([]int16, 100)),
		"mp3":   []byte("ID3\x04\x00\x00\x00\x00\x00\x00"),
	} {
		if _, err := Decode(bytes.NewReader(data)); !errors.Is(err, ErrNotOggOpus) {
			t.Errorf("%s: err = %v, want ErrNotOggOpus", name, err)
		}
	}
}

func TestWAV(t *testing.T) {
	pcm := []int16{0, 1, -1, math.MaxInt16, math.MinInt16}
	w := WAV(pcm)
	if len(w) != 44+2*len(pcm) || string(w[:4]) != "RIFF" || string(w[8:16]) != "WAVEfmt " || string(w[36:40]) != "data" {
		t.Fatalf("bad header % x", w[:44])
	}
	var hdr struct {
		Format, Channels   uint16
		Rate, ByteRate     uint32
		Align, BitsPerSamp uint16
	}
	binary.Read(bytes.NewReader(w[20:36]), binary.LittleEndian, &hdr)
	if hdr.Format != 1 || hdr.Channels != 1 || hdr.Rate != SampleRate || hdr.ByteRate != 2*SampleRate || hdr.Align != 2 || hdr.BitsPerSamp != 16 {
		t.Fatalf("bad fmt chunk %+v", hdr)
	}
	if got := binary.LittleEndian.Uint32(w[40:]); got != uint32(2*len(pcm)) {
		t.Fatalf("data size %d", got)
	}
	back := make([]int16, len(pcm))
	binary.Read(bytes.NewReader(w[44:]), binary.LittleEndian, back)
	for i := range pcm {
		if back[i] != pcm[i] {
			t.Fatalf("sample %d: %d != %d", i, back[i], pcm[i])
		}
	}
}

// BenchmarkDecodeVoiceNote decodes an 11 s voice note; seconds of audio per
// second of CPU is 11/(ns/op * 1e-9).
func BenchmarkDecodeVoiceNote(b *testing.B) {
	data, err := os.ReadFile("testdata/jfk-voip16k.ogg")
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := Decode(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}
