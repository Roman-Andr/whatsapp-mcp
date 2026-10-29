// Package content turns WhatsApp message protos into what the bridge stores
// and hands out: the text of a message (captions, locations, polls, events,
// business templates), its attachment, its reply metadata, and the MIME type
// and file-name helpers used for downloads and sends.
//
// It owns no state and does no I/O. It must not import other internal
// packages or the whatsmeow client: only the proto and types packages.
package content
