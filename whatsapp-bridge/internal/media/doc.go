// Package media fetches and prepares attachments: downloading a message's
// media into store/<chat>/ (verifying it, asking the phone to re-upload what
// the CDN forgot), the background auto-download Queue, profile pictures in
// store/avatars/, and the Ogg Opus analysis voice messages need.
//
// It talks to WhatsApp only through wa.Client and reads media details from
// the store; it never decides who may see a file (the bridge checks the read
// allowlist first). It imports nothing internal except store, content, jid
// and wa.
package media
