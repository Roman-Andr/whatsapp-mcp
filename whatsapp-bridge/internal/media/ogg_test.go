package media

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"strings"
	"testing"
)

// oggPage is one Ogg page with a single segment holding payload.
func oggPage(seq uint32, granule uint64, payload []byte) []byte {
	h := make([]byte, oggPageHeader)
	copy(h, "OggS")
	binary.LittleEndian.PutUint64(h[6:14], granule)
	binary.LittleEndian.PutUint32(h[18:22], seq)
	h[26] = 1 // one segment
	return append(append(h, byte(len(payload))), payload...)
}

// opusHead is an OpusHead packet laid out where AnalyzeOggOpus reads it:
// pre-skip and rate 18 and 20 bytes after the marker.
func opusHead(preSkip uint16, rate uint32) []byte {
	head := append([]byte("OpusHead"), make([]byte, 10)...)
	head = binary.LittleEndian.AppendUint16(head, preSkip)
	head = binary.LittleEndian.AppendUint32(head, rate)
	return append(head, make([]byte, 4)...)
}

func TestAnalyzeOggOpus(t *testing.T) {
	t.Parallel()
	// The header claims a 48 kHz rate in its own field; the granule counts
	// 48 kHz samples: 3.5 s rounds up to 4.
	voice := append(oggPage(0, 0, opusHead(312, opusSampleRate)), oggPage(1, 312+3*48000+24000, []byte("audio"))...)
	voice = append(voice, 0) // trailing byte so the last page is read
	hours := append(oggPage(0, 0, opusHead(0, opusSampleRate)), oggPage(1, 48000*3600, []byte("audio"))...)
	hours = append(hours, 0)
	noHead := append(oggPage(2, 0, []byte("not a head")), bytes.Repeat([]byte{0}, 1000)...)

	cases := []struct {
		name    string
		data    []byte
		seconds uint32
		debug   string
	}{
		{"duration from the granule", voice, 4, "found OpusHead"},
		{"capped at five minutes", hours, maxVoiceSeconds, "calculated Opus duration"},
		{"estimated without granule", noHead, minVoiceSeconds, "no valid granule position"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			seconds, waveform, err := AnalyzeOggOpus(c.data, log)
			if err != nil {
				t.Fatal(err)
			}
			if seconds != c.seconds || len(waveform) != 64 {
				t.Errorf("%d s, %d waveform bytes; log:\n%s", seconds, len(waveform), logs.String())
			}
			if !strings.Contains(logs.String(), "level=DEBUG") || !strings.Contains(logs.String(), c.debug) {
				t.Errorf("debug log %q lacks %q", logs.String(), c.debug)
			}
		})
	}
	t.Run("not an Ogg file", func(t *testing.T) {
		t.Parallel()
		if _, _, err := AnalyzeOggOpus([]byte("RIFF...."), nil); err == nil {
			t.Error("no error")
		}
	})
}

func TestPlaceholderWaveformDependsOnlyOnDuration(t *testing.T) {
	t.Parallel()
	a, b := placeholderWaveform(12), placeholderWaveform(12)
	if len(a) != 64 || string(a) != string(b) {
		t.Errorf("same duration, different waveforms:\n%v\n%v", a, b)
	}
	if string(placeholderWaveform(13)) == string(a) {
		t.Error("waveform ignores the duration")
	}
	for _, v := range placeholderWaveform(200) {
		if v > 100 {
			t.Fatalf("value %d out of WhatsApp's range", v)
		}
	}
}
