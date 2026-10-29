// Package mediaconv adapts the media/ packages and the ffmpeg and
// whisper-cli binaries to what the tools need: images scaled for the
// model, stickers, voice messages, video frames, transcription, and the
// bridge's media roots that decide which files may be sent.
//
// A Toolkit holds the conversions and is passed in; nothing is swapped
// globally. It reads bridge_meta through internal/store and does not talk
// to the bridge.
package mediaconv
