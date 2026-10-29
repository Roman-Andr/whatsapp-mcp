// Package events fans bridge events out: Event is the wire type SSE
// subscribers and the webhook receive, Hub delivers it to every subscriber
// without blocking, and Webhook POSTs it, signed, to a configured URL.
//
// It owns the subscriber set and the webhook queue and its worker. It must
// not import other internal packages: it does not know where events come
// from or who reads the stream.
package events
