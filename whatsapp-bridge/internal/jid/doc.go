// Package jid parses and normalises WhatsApp addresses (types.JID): the
// recipients API callers name by phone number or JID, and group JIDs.
//
// It is pure: no client, no store, no I/O. Its error texts reach API
// responses and start-up errors, so they are part of the contract.
package jid
