// Package audio decodes WhatsApp voice notes (Ogg/Opus) to the 16 kHz mono PCM
// that whisper wants, in pure Go: no ffmpeg, no libopus, no cgo.
//
// github.com/darui3018823/opus does the work; its
// decoder matches libopus on the RFC 8251 vectors in SILK, CELT and hybrid mode,
// so every Opus file WhatsApp (or anyone) produces decodes, not only the SILK
// voice profile. Opus decoders render natively at 16 kHz, so no resampler is
// needed either.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/darui3018823/opus"
	"github.com/darui3018823/opus/oggopus"
)

// SampleRate is the rate of every PCM slice this package returns.
const SampleRate = 16000

// opus timestamps (pre-skip, granule positions) are always in 48 kHz samples.
const ratio48k = 48000 / SampleRate

// ErrNotOggOpus reports a file that is not an Ogg stream carrying Opus, e.g. an
// MP3 or M4A audio message, which this package does not decode.
var ErrNotOggOpus = errors.New("not an Ogg/Opus file")

// DecodeFile decodes an Ogg/Opus file to 16 kHz mono PCM.
func DecodeFile(path string) ([]int16, error) {
	f, err := os.Open(path) //nolint:gosec // G304: decoding the file the caller names is the point
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Decode(f)
}

// Decode reads an Ogg/Opus stream and returns it as 16 kHz mono PCM: stereo is
// averaged, the header's output gain applied, pre-skip and end trimming honoured.
// A chained file (several logical streams one after another) is decoded whole.
func Decode(r io.Reader) ([]int16, error) {
	reader, err := oggopus.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotOggOpus, err)
	}
	var (
		out     []int16
		dec     *opus.Decoder
		link    = -1
		chans   int
		gain    float64
		buf     []int16
		decoded int
		preSkip int
	)
	for {
		pkt, err := reader.NextPacket()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A voice note cut short by a failed download still holds speech;
			// keep what decoded instead of losing all of it.
			if len(out) > 0 && errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, fmt.Errorf("read ogg: %w", err)
		}
		if pkt.LinkIndex != link {
			link = pkt.LinkIndex
			head := reader.Head
			if head.MappingFamily != 0 {
				return nil, fmt.Errorf("unsupported Opus channel mapping family %d", head.MappingFamily)
			}
			chans = int(head.Channels)
			if dec, err = opus.NewDecoder(SampleRate, chans); err != nil {
				return nil, err
			}
			gain = math.Pow(10, float64(head.OutputGain)/(256*20))
			buf = make([]int16, 5760*chans) // 120 ms at 48 kHz, the longest packet
			// Pre-skip is the encoder's warm-up and is always dropped at the
			// start of a link, as libopusfile and FFmpeg do. oggopus.Reader
			// waives it when the first page's granule is past zero, which
			// FFmpeg-written files (granule counted from the first real sample)
			// always are, so the Reader's DiscardStart is not trusted for it.
			preSkip = int(head.PreSkip) / ratio48k
		}
		n, err := dec.Decode(pkt.Data, buf)
		if err != nil {
			return nil, fmt.Errorf("decode packet %d: %w", decoded, err)
		}
		decoded++
		skip := min(preSkip, n)
		preSkip -= skip
		start := min(max(skip, pkt.DiscardStart/ratio48k), n)
		end := max(n-pkt.DiscardEnd/ratio48k, start)
		for i := start; i < end; i++ {
			var v float64
			for c := range chans {
				v += float64(buf[i*chans+c])
			}
			out = append(out, clamp16(v/float64(chans)*gain))
		}
	}
	if decoded == 0 {
		return nil, fmt.Errorf("%w: no audio packets", ErrNotOggOpus)
	}
	return out, nil
}

func clamp16(v float64) int16 {
	switch {
	case v >= math.MaxInt16:
		return math.MaxInt16
	case v <= math.MinInt16:
		return math.MinInt16
	}
	return int16(math.Round(v))
}

// WAV returns pcm as a 16-bit mono 16 kHz RIFF/WAVE file, the input format
// whisper.cpp reads without ffmpeg.
func WAV(pcm []int16) []byte {
	size := uint32(len(pcm) * 2) //nolint:gosec // G115: a voice note is far below 4 GiB, the RIFF limit
	le := binary.LittleEndian
	b := make([]byte, 0, 44+int(size))
	b = append(b, "RIFF"...)
	b = le.AppendUint32(b, 36+size)
	b = append(b, "WAVEfmt "...)
	// fmt chunk: PCM, mono, 16-bit
	b = le.AppendUint32(b, 16)
	b = le.AppendUint16(b, 1)
	b = le.AppendUint16(b, 1)
	b = le.AppendUint32(b, SampleRate)
	b = le.AppendUint32(b, SampleRate*2)
	b = le.AppendUint16(b, 2)
	b = le.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = le.AppendUint32(b, size)
	for _, v := range pcm {
		b = le.AppendUint16(b, uint16(v)) //nolint:gosec // G115: the two's-complement bits of the sample, as WAV stores them
	}
	return b
}
