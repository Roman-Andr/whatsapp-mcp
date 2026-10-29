package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
)

// Voice note limits: WhatsApp shows at least one second, and the bridge
// caps the estimate at five minutes so a broken file cannot claim hours.
const (
	minVoiceSeconds = 1
	maxVoiceSeconds = 300
)

// opusSampleRate is the rate Opus granule positions count in, whatever the
// input rate recorded in the header.
const opusSampleRate = 48000

// oggPageHeader is the fixed part of an Ogg page header, before the
// segment table.
const oggPageHeader = 27

// bytesPerSecondEstimate is the rough bitrate (16 kbit/s) used for a voice
// note without a usable granule position.
const bytesPerSecondEstimate = 2000.0

// AnalyzeOggOpus tries to extract duration and generate a simple waveform
// from an Ogg Opus file. The details of the analysis go to log at Debug; a
// nil log discards them.
func AnalyzeOggOpus(data []byte, log *slog.Logger) (duration uint32, waveform []byte, err error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	// Try to detect if this is a valid Ogg file by checking for the "OggS" signature
	// at the beginning of the file
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not a valid Ogg file (missing OggS signature)")
	}

	// Parse Ogg pages to find the last page with a valid granule position
	var lastGranule uint64
	var sampleRate uint32 = opusSampleRate
	var preSkip uint16
	var foundOpusHead bool

	// Scan through the file looking for Ogg pages
	for i := 0; i < len(data); {
		// Check if we have enough data to read Ogg page header
		if i+oggPageHeader >= len(data) {
			break
		}

		// Verify Ogg page signature
		if string(data[i:i+4]) != "OggS" {
			// Skip until next potential page
			i++
			continue
		}

		// Extract header fields
		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeqNum := binary.LittleEndian.Uint32(data[i+18 : i+22])
		numSegments := int(data[i+oggPageHeader-1])

		// Extract segment table
		if i+oggPageHeader+numSegments >= len(data) {
			break
		}
		segmentTable := data[i+oggPageHeader : i+oggPageHeader+numSegments]

		// Calculate page size
		pageSize := oggPageHeader + numSegments
		for _, segLen := range segmentTable {
			pageSize += int(segLen)
		}

		// Check if we're looking at an OpusHead packet (should be in first few pages)
		if !foundOpusHead && pageSeqNum <= 1 {
			// Look for "OpusHead" marker in this page
			pageData := data[i : i+pageSize]
			headPos := bytes.Index(pageData, []byte("OpusHead"))
			if headPos >= 0 && headPos+12 < len(pageData) {
				// Found OpusHead, extract sample rate and pre-skip
				// OpusHead format: Magic(8) + Version(1) + Channels(1) + PreSkip(2) + SampleRate(4) + ...
				headPos += 8 // Skip "OpusHead" marker
				// PreSkip is 2 bytes at offset 10
				if headPos+12 <= len(pageData) {
					preSkip = binary.LittleEndian.Uint16(pageData[headPos+10 : headPos+12])
					sampleRate = binary.LittleEndian.Uint32(pageData[headPos+12 : headPos+16])
					foundOpusHead = true
					log.Debug("found OpusHead", "sample_rate", sampleRate, "pre_skip", preSkip)
				}
			}
		}

		// Keep track of last valid granule position
		if granulePos != 0 {
			lastGranule = granulePos
		}

		// Move to next page
		i += pageSize
	}

	if !foundOpusHead {
		log.Debug("OpusHead not found, using default values")
	}

	// Calculate duration based on granule position
	if lastGranule > 0 {
		// Formula for duration: (lastGranule - preSkip) / sampleRate
		durationSeconds := float64(lastGranule-uint64(preSkip)) / float64(sampleRate)
		duration = uint32(math.Ceil(durationSeconds))
		log.Debug("calculated Opus duration from granule", "seconds", durationSeconds, "last_granule", lastGranule)
	} else {
		// Fallback to rough estimation if granule position not found
		log.Debug("no valid granule position found, using estimation")
		durationEstimate := float64(len(data)) / bytesPerSecondEstimate
		duration = uint32(durationEstimate)
	}

	duration = max(min(duration, maxVoiceSeconds), minVoiceSeconds)

	// Generate waveform
	waveform = placeholderWaveform(duration)

	log.Debug("Ogg Opus analysis", "bytes", len(data), "duration_seconds", duration, "waveform_bytes", len(waveform))

	return duration, waveform, nil
}

// placeholderWaveform generates a synthetic waveform for WhatsApp voice messages
// that appears natural with some variability based on the duration
func placeholderWaveform(duration uint32) []byte {
	// WhatsApp expects a 64-byte waveform for voice messages
	const waveformLength = 64
	waveform := make([]byte, waveformLength)

	// A generator seeded with the duration gives the same waveform for the
	// same duration. (The global rand.Seed is a no-op since Go 1.24.)
	rng := rand.New(rand.NewSource(int64(duration))) //nolint:gosec // G404: a decorative waveform, not a secret

	// Create a more natural looking waveform with some patterns and variability
	// rather than completely random values

	// Base amplitude and frequency - longer messages get faster frequency
	baseAmplitude := 35.0
	frequencyFactor := float64(min(int(duration), 120)) / 30.0

	for i := range waveform {
		// Position in the waveform (normalized 0-1)
		pos := float64(i) / float64(waveformLength)

		// Create a wave pattern with some randomness
		// Use multiple sine waves of different frequencies for more natural look
		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)

		// Add some randomness to make it look more natural
		val += (rng.Float64() - 0.5) * 15

		// Add some fade-in and fade-out effects
		fadeInOut := math.Sin(pos * math.Pi)
		val *= 0.7 + 0.3*fadeInOut

		// Center around 50 (typical voice baseline)
		val += 50

		// Ensure values stay within WhatsApp's expected range (0-100)
		val = max(min(val, 100), 0)

		waveform[i] = byte(val)
	}

	return waveform
}
