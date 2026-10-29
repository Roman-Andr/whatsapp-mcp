package main

// Ingest contract: synthetic whatsmeow events through the bridge's event
// handler, then every table of messages.db and every published event,
// against testdata/contract/ingest/<case>.golden.

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/gcmutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"
	"google.golang.org/protobuf/proto"
)

// ingestT0 is the fixture time of the ingest cases.
var ingestT0 = time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC)

func atMin(m int) time.Time { return ingestT0.Add(time.Duration(m) * time.Minute) }

var (
	zedLID   = types.NewJID("99990000005", types.HiddenUserServer) // unknown group member
	carolLID = types.NewJID("99990000006", types.HiddenUserServer)
	group    = groupJID
)

func msgEvt(id string, chat, sender types.JID, fromMe bool, ts time.Time, m *waE2E.Message) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: fromMe, IsGroup: chat.Server == types.GroupServer},
			ID:            id, Timestamp: ts,
		},
		Message: m,
	}
}

func withPush(evt *events.Message, name string) *events.Message {
	evt.Info.PushName = name
	return evt
}

func text(s string) *waE2E.Message { return &waE2E.Message{Conversation: proto.String(s)} }

func key(chat, participant types.JID, fromMe bool, id string) *waCommon.MessageKey {
	k := &waCommon.MessageKey{RemoteJID: proto.String(chat.String()), FromMe: proto.Bool(fromMe), ID: proto.String(id)}
	if !participant.IsEmpty() {
		k.Participant = proto.String(participant.String())
	}
	return k
}

// sealSecret encrypts the way a WhatsApp client encrypts a vote, an event
// response or an edit with the original message's secret (whatsmeow's
// msgsecret.go), with a fixed IV so the fixture is stable.
func sealSecret(t *testing.T, secret []byte, useCase, origID string, origSender, modSender types.JID, plaintext []byte, withAD bool) (payload, iv []byte) {
	t.Helper()
	info := origID + origSender.ToNonAD().String() + modSender.ToNonAD().String() + useCase
	k := hkdfutil.SHA256(secret, nil, []byte(info), 32)
	var ad []byte
	if withAD {
		ad = []byte(origID + "\x00" + modSender.ToNonAD().String())
	}
	iv = []byte("contract-iv!")
	payload, err := gcmutil.Encrypt(k, iv, plaintext, ad)
	if err != nil {
		t.Fatal(err)
	}
	return payload, iv
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// runIngest builds a linked bridge, runs steps and compares the database,
// the published events and the audit log with ingest/<name>.golden.
func runIngest(t *testing.T, name string, o contractOpts, steps func(e *contractEnv)) {
	t.Helper()
	o.linked = true
	e := newContractEnv(t, o)
	sub := e.subscribe()
	steps(e)
	var sb strings.Builder
	sb.WriteString(e.dumpDB())
	sb.WriteString("== events\n")
	for _, evt := range drainEvents(sub) {
		data, err := json.Marshal(evt)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(data)
		sb.WriteString("\n")
	}
	if lines := e.auditLines(); len(lines) > 0 {
		sb.WriteString("== audit.log\n" + strings.Join(lines, "\n") + "\n")
	}
	checkGolden(t, "ingest/"+name+".golden", e.normalise(sb.String()))
}

func TestContractIngest(t *testing.T) {
	cases := []struct {
		name  string
		opts  contractOpts
		steps func(e *contractEnv)
	}{
		{"direct-text", contractOpts{}, func(e *contractEnv) {
			e.ingest(withPush(msgEvt("D1", alicePN, alicePN, false, atMin(0), text("hello from Alice")), "Alice Push"))
			e.ingest(msgEvt("D2", alicePN, ownPN, true, atMin(1), text("hi Alice")))
			// Bob is only known by push name; Dave not at all.
			e.ingest(withPush(msgEvt("D3", bobPN, bobPN, false, atMin(2), text("hey")), "Bobby"))
			dave := types.NewJID("79990000007", types.DefaultUserServer)
			e.ingest(withPush(msgEvt("D4", dave, dave, false, atMin(3), text("who am I")), "Dave Push"))
			// Alice by LID lands in her phone-number chat; so do notes to self.
			e.ingest(msgEvt("D5", aliceLID, aliceLID, false, atMin(4), text("via LID")))
			e.ingest(msgEvt("D6", ownLID.ToNonAD(), ownLID, true, atMin(5), text("note to self")))
			// A repeated delivery does not count twice.
			e.ingest(withPush(msgEvt("D1", alicePN, alicePN, false, atMin(0), text("hello from Alice")), "Alice Push"))
			// Nothing to store: no text, no media.
			e.ingest(msgEvt("D7", alicePN, alicePN, false, atMin(6), &waE2E.Message{}))
			e.ingest(msgEvt("D8", alicePN, alicePN, false, atMin(6), nil))
		}},
		{"group-text", contractOpts{}, func(e *contractEnv) {
			e.exec(`INSERT INTO chats (jid, name, last_message_time) VALUES ('120363000000000001@g.us', 'Depot group', '2025-01-01 00:00:00+00:00')`)
			e.ingest(withPush(msgEvt("G1", group, zedLID, false, atMin(0), text("hi all")), "Zed"))
			e.ingest(withPush(msgEvt("G2", group, aliceLID, false, atMin(1), text("hi Zed")), "Alice Push"))
			evt := msgEvt("G3", group, carolLID, false, atMin(2), text("alt sender"))
			evt.Info.SenderAlt = bobPN
			e.ingest(evt)
			e.ingest(msgEvt("G4", group, ownLID, true, atMin(3), text("mine")))
			// A group the bridge has no name for is named after its id.
			e.ingest(msgEvt("G5", types.NewJID("120363000000000009", types.GroupServer), zedLID, false, atMin(4), text("unnamed group")))
			// Status updates are stored but never counted as unread.
			e.ingest(withPush(msgEvt("S1", types.StatusBroadcastJID, alicePN, false, atMin(5), text("my status")), "Alice Push"))
		}},
		{"reply-mentions", contractOpts{}, func(e *contractEnv) {
			e.ingest(msgEvt("Q1", group, aliceLID, false, atMin(0), text("original")))
			e.ingest(msgEvt("R1", group, zedLID, false, atMin(1), &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String("@99990000002 agreed"),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID: proto.String("Q1"), Participant: proto.String(aliceLID.String()),
					QuotedMessage: text("original"),
					MentionedJID:  []string{aliceLID.String(), bobPN.String()},
				},
			}}))
			// A reply to a captionless image shows the media type.
			e.ingest(msgEvt("R2", group, zedLID, false, atMin(2), &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String("nice photo"),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID: proto.String("IMG"), Participant: proto.String(aliceLID.String()),
					QuotedMessage: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: proto.String("image/jpeg")}},
				},
			}}))
		}},
		{"media", contractOpts{}, func(e *contractEnv) {
			hash := func(b byte) []byte { return []byte(strings.Repeat(string(rune(b)), 32)) }
			e.ingest(msgEvt("IMG", alicePN, alicePN, false, atMin(0), &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
				Caption: proto.String("the depot"), Mimetype: proto.String("image/jpeg"),
				URL: proto.String("https://mmg.whatsapp.net/v/t62.7118-24/img?ccb=11"), DirectPath: proto.String("/v/t62.7118-24/img?ccb=11"),
				MediaKey: hash('k'), FileSHA256: hash('s'), FileEncSHA256: hash('e'), FileLength: proto.Uint64(54321),
				Width: proto.Uint32(1280), Height: proto.Uint32(720),
			}}))
			e.ingest(msgEvt("PTT", alicePN, alicePN, false, atMin(1), &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
				Mimetype: proto.String("audio/ogg; codecs=opus"), PTT: proto.Bool(true), Seconds: proto.Uint32(7),
				DirectPath: proto.String("/v/t62.7117-24/ptt"), MediaKey: hash('k'), FileSHA256: hash('s'), FileEncSHA256: hash('e'), FileLength: proto.Uint64(4096),
			}}))
			e.ingest(msgEvt("DOC", alicePN, alicePN, false, atMin(2), &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
				Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
					FileName: proto.String("../../route sheet.pdf"), Caption: proto.String("routes for Monday"),
					Mimetype: proto.String("application/pdf"), FileLength: proto.Uint64(99999),
				}},
			}}))
			e.ingest(msgEvt("STK", alicePN, alicePN, false, atMin(3), &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
				Mimetype: proto.String("image/webp"), IsAnimated: proto.Bool(true), Width: proto.Uint32(512), Height: proto.Uint32(512),
			}}))
			e.ingest(msgEvt("VID", alicePN, ownPN, true, atMin(4), &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
				Mimetype: proto.String("video/mp4"), GifPlayback: proto.Bool(true), Seconds: proto.Uint32(3),
			}}))
			e.ingest(msgEvt("PTV", alicePN, alicePN, false, atMin(5), &waE2E.Message{PtvMessage: &waE2E.VideoMessage{
				Mimetype: proto.String("video/mp4"), Seconds: proto.Uint32(12),
			}}))
		}},
		{"location-contact", contractOpts{}, func(e *contractEnv) {
			e.ingest(msgEvt("LOC", alicePN, alicePN, false, atMin(0), &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
				DegreesLatitude: proto.Float64(55.751244), DegreesLongitude: proto.Float64(37.618423),
				Name: proto.String("North depot"), Address: proto.String("1 Main st"),
			}}))
			e.ingest(msgEvt("LIVE", alicePN, alicePN, false, atMin(1), &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{
				DegreesLatitude: proto.Float64(1.5), DegreesLongitude: proto.Float64(-2.25), Caption: proto.String("on my way"),
			}}))
			e.ingest(msgEvt("VCARD", alicePN, alicePN, false, atMin(2), &waE2E.Message{ContactMessage: &waE2E.ContactMessage{
				DisplayName: proto.String("Bob"), Vcard: proto.String("BEGIN:VCARD\nVERSION:3.0\nFN:Bob\nEND:VCARD"),
			}}))
			e.ingest(msgEvt("VCARDS", alicePN, alicePN, false, atMin(3), &waE2E.Message{ContactsArrayMessage: &waE2E.ContactsArrayMessage{
				Contacts: []*waE2E.ContactMessage{{DisplayName: proto.String("Bob")}, {DisplayName: proto.String("Carol")}},
			}}))
		}},
		{"poll-vote", contractOpts{}, func(e *contractEnv) {
			secret := []byte("0123456789abcdef0123456789abcdef")
			e.ingest(msgEvt("P1", group, aliceLID, false, atMin(0), &waE2E.Message{
				PollCreationMessageV3: &waE2E.PollCreationMessage{
					Name:                   proto.String("Depot?"),
					Options:                []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("North")}, {OptionName: proto.String("South")}},
					SelectableOptionsCount: proto.Uint32(1),
				},
				MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret},
			}))
			e.putMessageSecret(group, aliceLID, "P1", secret)
			vote := func(id string, voter types.JID, options ...string) {
				plain := mustMarshal(e.t, &waE2E.PollVoteMessage{SelectedOptions: hashOptions(options)})
				payload, iv := sealSecret(e.t, secret, "Poll Vote", "P1", aliceLID, voter, plain, true)
				e.ingest(msgEvt(id, group, voter, false, atMin(5), &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{
					PollCreationMessageKey: key(group, aliceLID, false, "P1"),
					Vote:                   &waE2E.PollEncValue{EncPayload: payload, EncIV: iv},
					SenderTimestampMS:      proto.Int64(atMin(5).UnixMilli()),
				}}))
			}
			vote("V1", carolLID, "South")
			vote("V2", zedLID, "North", "East")
			// A vote that does not decrypt is dropped.
			e.ingest(msgEvt("V3", group, zedLID, false, atMin(6), &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{
				PollCreationMessageKey: key(group, aliceLID, false, "P1"),
				Vote:                   &waE2E.PollEncValue{EncPayload: []byte("garbage-garbage-garbage"), EncIV: []byte("contract-iv!")},
			}}))
		}},
		{"event-response", contractOpts{}, func(e *contractEnv) {
			secret := []byte("fedcba9876543210fedcba9876543210")
			e.ingest(msgEvt("EV1", group, aliceLID, false, atMin(0), &waE2E.Message{
				EventMessage: &waE2E.EventMessage{
					Name: proto.String("Depot meeting"), Description: proto.String("Bring the route sheets"),
					StartTime: proto.Int64(time.Date(2025, 3, 5, 18, 0, 0, 0, time.UTC).Unix()),
					EndTime:   proto.Int64(time.Date(2025, 3, 5, 20, 0, 0, 0, time.UTC).Unix()),
					Location:  &waE2E.LocationMessage{Name: proto.String("North depot")},
				},
				MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret},
			}))
			e.putMessageSecret(group, aliceLID, "EV1", secret)
			plain := mustMarshal(e.t, &waE2E.EventResponseMessage{
				Response: waE2E.EventResponseMessage_GOING.Enum(), TimestampMS: proto.Int64(atMin(7).UnixMilli()), ExtraGuestCount: proto.Int32(2),
			})
			payload, iv := sealSecret(e.t, secret, "Event Response", "EV1", aliceLID, carolLID, plain, true)
			e.ingest(withPush(msgEvt("ER1", group, carolLID, false, atMin(7), &waE2E.Message{EncEventResponseMessage: &waE2E.EncEventResponseMessage{
				EventCreationMessageKey: key(group, aliceLID, false, "EV1"), EncPayload: payload, EncIV: iv,
			}}), "Carol"))
		}},
		{"business", contractOpts{}, func(e *contractEnv) {
			for i, c := range contentCorpus {
				if !c.business {
					continue
				}
				e.ingest(msgEvt("BIZ-"+c.name, alicePN, alicePN, false, atMin(i), c.msg))
			}
		}},
		{"edits", contractOpts{}, func(e *contractEnv) {
			secret := []byte("abcdefabcdefabcdefabcdefabcdef00")
			e.ingest(msgEvt("E1", group, aliceLID, false, atMin(0), &waE2E.Message{
				Conversation:       proto.String("first draft"),
				MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret},
			}))
			e.ingest(msgEvt("E2", group, ownLID, true, atMin(1), text("my draft")))
			e.ingest(msgEvt("E3", alicePN, alicePN, false, atMin(2), text("direct draft")))
			// The author edits: protocol message, and the newer encrypted form.
			edit := revisionEvt(group, aliceLID, false, waE2E.ProtocolMessage_MESSAGE_EDIT, "E1", "second draft")
			edit.Info.ID, edit.Info.Timestamp = "EDIT1", atMin(3)
			e.ingest(edit)
			e.putMessageSecret(group, aliceLID, "E1", secret)
			plain := mustMarshal(e.t, &waE2E.Message{Conversation: proto.String("final text")})
			payload, iv := sealSecret(e.t, secret, "Message Edit", "E1", aliceLID, aliceLID, plain, false)
			e.ingest(msgEvt("EDIT2", group, aliceLID, false, atMin(4), &waE2E.Message{SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
				TargetMessageKey: key(group, aliceLID, false, "E1"), EncPayload: payload, EncIV: iv,
				SecretEncType: waE2E.SecretEncryptedMessage_MESSAGE_EDIT.Enum(),
			}}))
			// Own edit from the phone; an edit of a direct chat by its author
			// addressed by LID.
			own := revisionEvt(group, ownLID, true, waE2E.ProtocolMessage_MESSAGE_EDIT, "E2", "my final")
			own.Info.ID, own.Info.Timestamp = "EDIT3", atMin(5)
			e.ingest(own)
			direct := revisionEvt(aliceLID, aliceLID, false, waE2E.ProtocolMessage_MESSAGE_EDIT, "E3", "direct final")
			direct.Info.ID, direct.Info.Timestamp = "EDIT4", atMin(6)
			e.ingest(direct)
		}},
		{"edits-foreign", contractOpts{}, func(e *contractEnv) {
			e.ingest(msgEvt("F1", group, aliceLID, false, atMin(0), text("Alice's words")))
			e.ingest(msgEvt("F2", group, ownLID, true, atMin(1), text("my words")))
			for i, target := range []string{"F1", "F2", "UNKNOWN"} {
				forged := revisionEvt(group, zedLID, false, waE2E.ProtocolMessage_MESSAGE_EDIT, target, "forged")
				forged.Info.ID, forged.Info.Timestamp = "FORGE"+target, atMin(2+i)
				e.ingest(forged)
			}
			empty := revisionEvt(group, aliceLID, false, waE2E.ProtocolMessage_MESSAGE_EDIT, "F1", "")
			empty.Info.ID = "EMPTY"
			e.ingest(empty)
			// An encrypted edit without the secret, and one of another kind.
			e.ingest(msgEvt("ENC", group, aliceLID, false, atMin(6), &waE2E.Message{SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
				TargetMessageKey: key(group, aliceLID, false, "F1"), EncPayload: []byte("x"), EncIV: []byte("contract-iv!"),
				SecretEncType: waE2E.SecretEncryptedMessage_MESSAGE_EDIT.Enum(),
			}}))
			e.ingest(msgEvt("ENC2", group, aliceLID, false, atMin(7), &waE2E.Message{SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
				TargetMessageKey: key(group, aliceLID, false, "F1"), SecretEncType: waE2E.SecretEncryptedMessage_EVENT_EDIT.Enum(),
			}}))
		}},
		{"revokes", contractOpts{}, func(e *contractEnv) {
			e.ingest(msgEvt("X1", group, aliceLID, false, atMin(0), text("oops")))
			e.ingest(msgEvt("X2", group, ownLID, true, atMin(1), text("mine, oops")))
			e.ingest(msgEvt("X3", group, zedLID, false, atMin(2), text("Zed's")))
			e.ingest(msgEvt("X4", alicePN, alicePN, false, atMin(3), text("direct oops")))
			for i, r := range []struct {
				target string
				sender types.JID
				fromMe bool
			}{
				{"X1", aliceLID, false}, // the author
				{"X2", ownLID, true},    // ourselves from the phone
				// An admin deleting someone else's message: deciding that
				// needs WhatsApp, so offline it is ignored.
				{"X3", aliceLID, false},
				{"X4", alicePN, false},
				{"", aliceLID, false},
			} {
				evt := revisionEvt(group, r.sender, r.fromMe, waE2E.ProtocolMessage_REVOKE, r.target, "")
				if r.target == "X4" {
					evt = revisionEvt(alicePN, r.sender, r.fromMe, waE2E.ProtocolMessage_REVOKE, r.target, "")
				}
				evt.Info.ID, evt.Info.Timestamp = "REV"+r.target, atMin(10+i)
				e.ingest(evt)
			}
		}},
		{"reactions", contractOpts{}, func(e *contractEnv) {
			e.ingest(msgEvt("M1", group, aliceLID, false, atMin(0), text("react to me")))
			e.ingest(msgEvt("M2", alicePN, ownPN, true, atMin(0), text("direct")))
			react := func(id string, chat, sender types.JID, fromMe bool, target *waCommon.MessageKey, emoji string, m int) {
				e.ingest(withPush(msgEvt(id, chat, sender, fromMe, atMin(m), &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
					Key: target, Text: proto.String(emoji), SenderTimestampMS: proto.Int64(atMin(m).UnixMilli()),
				}}), "Zed"))
			}
			react("RA1", group, zedLID, false, key(group, aliceLID, false, "M1"), "👍", 1)
			react("RA2", group, ownLID, true, key(group, aliceLID, false, "M1"), "❤️", 2)
			react("RA3", group, zedLID, false, key(group, aliceLID, false, "M1"), "😂", 3) // replaces RA1
			react("RA4", group, ownLID, true, key(group, aliceLID, false, "M1"), "", 4)   // removes ours
			// Alice reacts by LID in the direct chat stored under her number.
			react("RA5", aliceLID, aliceLID, false, key(aliceLID, types.EmptyJID, false, "M2"), "🙏", 5)
			react("RA6", group, zedLID, false, &waCommon.MessageKey{}, "👍", 6) // no target
		}},
		{"undecryptable", contractOpts{}, func(e *contractEnv) {
			info := types.MessageInfo{
				MessageSource: types.MessageSource{Chat: alicePN, Sender: alicePN},
				ID:            "U1", Timestamp: atMin(0), PushName: "Alice Push",
			}
			e.ingest(&events.UndecryptableMessage{Info: info})
			e.ingest(&events.UndecryptableMessage{Info: info}) // a second failure adds nothing
			hidden := info
			hidden.ID = "U2"
			e.ingest(&events.UndecryptableMessage{Info: hidden, DecryptFailMode: events.DecryptFailHide})
			mine := info
			mine.ID, mine.IsFromMe = "U3", true
			e.ingest(&events.UndecryptableMessage{Info: mine})
			// The resend replaces the placeholder.
			e.ingest(&events.Message{Info: info, Message: text("the real text")})
		}},
		{"history-sync", contractOpts{}, func(e *contractEnv) {
			web := func(chat, participant types.JID, fromMe bool, id string, ts time.Time, m *waE2E.Message) *waHistorySync.HistorySyncMsg {
				w := &waWeb.WebMessageInfo{Key: key(chat, types.EmptyJID, fromMe, id), Message: m, MessageTimestamp: proto.Uint64(uint64(ts.Unix()))}
				if !participant.IsEmpty() {
					w.Participant = proto.String(participant.String())
				}
				return &waHistorySync.HistorySyncMsg{Message: w}
			}
			poll := web(group, aliceLID, false, "HP1", atMin(2), &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{
				Name: proto.String("Lunch?"), Options: []*waE2E.PollCreationMessage_Option{{OptionName: proto.String("Soup")}, {OptionName: proto.String("Salad")}},
			}})
			poll.Message.PollUpdates = []*waWeb.PollUpdate{{
				PollUpdateMessageKey: key(group, carolLID, false, "HV1"),
				Vote:                 &waE2E.PollVoteMessage{SelectedOptions: hashOptions([]string{"Salad"})},
				SenderTimestampMS:    proto.Int64(atMin(3).UnixMilli()),
			}}
			poll.Message.Reactions = []*waWeb.Reaction{{
				Key: key(group, zedLID, false, "HR1"), Text: proto.String("🔥"), SenderTimestampMS: proto.Int64(atMin(4).UnixMilli()),
			}}
			poll.Message.Starred = proto.Bool(true)
			event := web(group, aliceLID, false, "HE1", atMin(5), &waE2E.Message{EventMessage: &waE2E.EventMessage{Name: proto.String("Party")}})
			event.Message.EventResponses = []*waWeb.EventResponse{{
				EventResponseMessageKey: key(group, types.EmptyJID, true, "HER1"),
				TimestampMS:             proto.Int64(atMin(6).UnixMilli()),
				EventResponseMessage:    &waE2E.EventResponseMessage{Response: waE2E.EventResponseMessage_MAYBE.Enum()},
			}}
			e.ingest(&events.HistorySync{Data: &waHistorySync.HistorySync{
				SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
				Conversations: []*waHistorySync.Conversation{
					{
						ID: proto.String(group.String()), Name: proto.String("Depot group"),
						ConversationTimestamp: proto.Uint64(uint64(atMin(6).Unix())),
						UnreadCount:           proto.Uint32(3), Archived: proto.Bool(true), Pinned: proto.Uint32(1700000000),
						MuteEndTime: proto.Uint64(^uint64(0)),
						Messages: []*waHistorySync.HistorySyncMsg{
							web(group, aliceLID, false, "H1", atMin(0), text("history one")),
							web(group, types.EmptyJID, true, "H2", atMin(1), text("history mine")),
							poll, event,
							{Message: &waWeb.WebMessageInfo{Key: key(group, types.EmptyJID, false, "H0")}}, // no timestamp: skipped
						},
					},
					{
						ID: proto.String(aliceLID.String()), DisplayName: proto.String("ignored for direct chats"),
						ConversationTimestamp: proto.Uint64(uint64(atMin(9).Unix())),
						MarkedAsUnread:        proto.Bool(true), MuteEndTime: proto.Uint64(uint64(atMin(600).Unix())),
						Messages: []*waHistorySync.HistorySyncMsg{
							web(aliceLID, types.EmptyJID, false, "H3", atMin(8), text("direct history")),
						},
					},
					{ID: proto.String("not a jid@@")},
				},
			}})
			// An on-demand page carries no chat state and is announced.
			e.ingest(&events.HistorySync{Data: &waHistorySync.HistorySync{
				SyncType: waHistorySync.HistorySync_ON_DEMAND.Enum(),
				Conversations: []*waHistorySync.Conversation{{
					ID: proto.String(bobPN.String()), UnreadCount: proto.Uint32(9),
					Messages: []*waHistorySync.HistorySyncMsg{web(bobPN, types.EmptyJID, false, "H4", atMin(-60), text("older page"))},
				}},
			}})
		}},
		{"unread-receipts", contractOpts{}, func(e *contractEnv) {
			e.ingest(msgEvt("N1", alicePN, alicePN, false, atMin(0), text("one")))
			e.ingest(msgEvt("N2", alicePN, alicePN, false, atMin(1), text("two")))
			e.ingest(msgEvt("N3", bobPN, bobPN, false, atMin(2), text("three")))
			e.ingest(msgEvt("N4", group, zedLID, false, atMin(3), text("four")))
			e.ingest(msgEvt("N5", group, zedLID, false, atMin(4), text("five")))
			// Read on another device.
			e.ingest(&events.Receipt{MessageSource: types.MessageSource{Chat: aliceLID, Sender: ownPN, IsFromMe: true},
				MessageIDs: []string{"N2"}, Timestamp: atMin(5), Type: types.ReceiptTypeReadSelf})
			// Someone else's read receipt changes nothing.
			e.ingest(&events.Receipt{MessageSource: types.MessageSource{Chat: bobPN, Sender: bobPN},
				MessageIDs: []string{"N3"}, Timestamp: atMin(5), Type: types.ReceiptTypeRead})
			e.ingest(&events.MarkChatAsRead{JID: bobPN, Timestamp: atMin(6), Action: &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(true)}})
			e.ingest(&events.MarkChatAsRead{JID: alicePN, Timestamp: atMin(7), Action: &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(false)}})
			// Writing in a chat clears it.
			e.ingest(msgEvt("N6", group, ownLID, true, atMin(8), text("six")))
		}},
		{"group-notices", contractOpts{welcomes: map[string]string{group.String(): "Welcome {name}! {mention}"}}, func(e *contractEnv) {
			e.exec(`INSERT INTO chats (jid, name, last_message_time) VALUES ('120363000000000001@g.us', 'Depot group', '2025-01-01 00:00:00+00:00')`)
			admin := aliceLID
			adminPN := alicePN
			e.ingest(&events.GroupInfo{
				JID: group, Sender: &admin, SenderPN: &adminPN, Timestamp: atMin(0),
				Join: []types.JID{zedLID}, Leave: []types.JID{carolLID}, Promote: []types.JID{zedLID}, Demote: []types.JID{bobPN},
			})
			e.ingest(&events.GroupInfo{JID: group, Timestamp: atMin(1), Join: []types.JID{carolLID}, JoinReason: "invite"})
			e.ingest(&events.GroupInfo{JID: group, Sender: &admin, Timestamp: atMin(2), Leave: []types.JID{aliceLID}})
			e.ingest(&events.GroupInfo{
				JID: group, Sender: &admin, Timestamp: atMin(3),
				Name:     &types.GroupName{Name: "Depot crew"},
				Topic:    &types.GroupTopic{Topic: "Shifts and routes"},
				Announce: &types.GroupAnnounce{IsAnnounce: true},
			})
			e.ingest(&events.GroupInfo{JID: group, Timestamp: atMin(4)}) // nothing to say
			e.ingest(&events.JoinedGroup{GroupInfo: types.GroupInfo{JID: types.NewJID("120363000000000008", types.GroupServer),
				GroupName: types.GroupName{Name: "New team"}}})
		}},
		{"calls", contractOpts{}, func(e *contractEnv) {
			meta := types.BasicCallMeta{From: alicePN, CallCreator: alicePN, CallID: "C1", Timestamp: atMin(0)}
			e.ingest(&events.CallOffer{BasicCallMeta: meta, Data: &waBinary.Node{Tag: "offer", Content: []waBinary.Node{{Tag: "video"}}}})
			e.ingest(&events.CallTerminate{BasicCallMeta: meta, Reason: "timeout"})
			meta.CallID, meta.Timestamp = "C2", atMin(5)
			e.ingest(&events.CallOffer{BasicCallMeta: meta})
			e.ingest(&events.CallAccept{BasicCallMeta: meta})
			e.ingest(&events.CallTerminate{BasicCallMeta: meta, Reason: "busy"})
			inGroup := types.BasicCallMeta{From: zedLID, CallCreator: zedLID, CallID: "C3", Timestamp: atMin(10), GroupJID: groupJID}
			e.ingest(&events.CallOfferNotice{BasicCallMeta: inGroup, Media: "video", Type: "group"})
			e.ingest(&events.CallTerminate{BasicCallMeta: types.BasicCallMeta{CallID: "UNKNOWN"}})
		}},
		{"chat-state", contractOpts{}, func(e *contractEnv) {
			e.ingest(msgEvt("CS1", alicePN, alicePN, false, atMin(0), text("hello")))
			e.ingest(msgEvt("CS2", group, zedLID, false, atMin(1), text("group hello")))
			e.ingest(&events.Archive{JID: aliceLID, Timestamp: atMin(2), Action: &waSyncAction.ArchiveChatAction{Archived: proto.Bool(true)}})
			e.ingest(&events.Pin{JID: group, Timestamp: atMin(3), Action: &waSyncAction.PinAction{Pinned: proto.Bool(true)}})
			e.ingest(&events.Mute{JID: group, Timestamp: atMin(4), Action: &waSyncAction.MuteAction{Muted: proto.Bool(true), MuteEndTimestamp: proto.Int64(atMin(120).UnixMilli())}})
			e.ingest(&events.Mute{JID: alicePN, Timestamp: atMin(5), Action: &waSyncAction.MuteAction{Muted: proto.Bool(true)}})
			e.ingest(&events.DeleteChat{JID: bobPN, Timestamp: atMin(6), Action: &waSyncAction.DeleteChatAction{}})
			e.ingest(&events.LabelEdit{LabelID: "5", Timestamp: atMin(7), Action: &waSyncAction.LabelEditAction{Name: proto.String("Customers"), Color: proto.Int32(3)}})
			e.ingest(&events.LabelEdit{LabelID: "6", Timestamp: atMin(7), Action: &waSyncAction.LabelEditAction{Name: proto.String("Old"), Deleted: proto.Bool(true)}})
			e.ingest(&events.LabelAssociationChat{JID: aliceLID, LabelID: "5", Timestamp: atMin(8), Action: &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)}})
			e.ingest(&events.LabelAssociationChat{JID: group, LabelID: "5", Timestamp: atMin(8), Action: &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(true)}})
			e.ingest(&events.LabelAssociationChat{JID: group, LabelID: "5", Timestamp: atMin(9), Action: &waSyncAction.LabelAssociationAction{Labeled: proto.Bool(false)}})
			e.ingest(&events.Star{ChatJID: aliceLID, SenderJID: aliceLID, MessageID: "CS1", Timestamp: atMin(10), Action: &waSyncAction.StarAction{Starred: proto.Bool(true)}})
			e.ingest(&events.Star{ChatJID: group, SenderJID: zedLID, MessageID: "CS2", Timestamp: atMin(10), Action: &waSyncAction.StarAction{Starred: proto.Bool(true)}})
			e.ingest(&events.Star{ChatJID: group, SenderJID: zedLID, MessageID: "CS2", Timestamp: atMin(11), Action: &waSyncAction.StarAction{Starred: proto.Bool(false)}})
			// A new message brings a deleted chat back.
			e.ingest(msgEvt("CS3", bobPN, bobPN, false, atMin(12), text("I'm back")))
		}},
		{"lid-merge", contractOpts{}, func(e *contractEnv) {
			// Rows stored under Alice's LID before the mapping was known.
			e.exec(`INSERT INTO chats (jid, name, last_message_time) VALUES
				('99990000002@lid', '', '2025-02-01 10:00:00+00:00'),
				('79990000002@s.whatsapp.net', 'Alice Full', '2025-02-02 10:00:00+00:00'),
				('99990000009@lid', 'Unknown', '2025-02-01 10:00:00+00:00')`)
			e.exec(`INSERT INTO messages (id, chat_jid, sender, sender_jid, content, timestamp, is_from_me) VALUES
				('L1', '99990000002@lid', '99990000002', '99990000002@lid', 'under the LID', '2025-02-01 10:00:00+00:00', 0),
				('L2', '79990000002@s.whatsapp.net', '79990000002', '79990000002@s.whatsapp.net', 'under the number', '2025-02-02 10:00:00+00:00', 0),
				('L3', '99990000009@lid', '99990000009', '99990000009@lid', 'no mapping', '2025-02-01 10:00:00+00:00', 0)`)
			e.exec(`INSERT INTO reactions (message_id, chat_jid, sender_jid, sender_name, emoji, timestamp)
				VALUES ('L1', '99990000002@lid', '99990000001@lid', 'Me', '👍', '2025-02-01 10:01:00+00:00')`)
			e.exec(`INSERT INTO event_responses (message_id, chat_jid, responder_jid, response, timestamp)
				VALUES ('L1', '99990000002@lid', '99990000001@lid', 'going', '2025-02-01 10:01:00+00:00')`)
			e.exec(`INSERT INTO chat_labels (chat_jid, label_id) VALUES ('99990000002@lid', '5')`)
			e.ingest(&events.Connected{})
		}},
		{"session-events", contractOpts{}, func(e *contractEnv) {
			e.ingest(&events.Connected{})
			e.ingest(&events.Connected{}) // unchanged: not published again
			e.ingest(&events.Disconnected{})
			e.ingest(&events.StreamReplaced{})
			e.ingest(&events.Presence{From: alicePN, Unavailable: true, LastSeen: atMin(0)})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runIngest(t, c.name, c.opts, c.steps) })
	}
}

// revisionEvt is an edit or revoke of target sent by sender.
func revisionEvt(chat, sender types.JID, fromMe bool, kind waE2E.ProtocolMessage_Type, target, newText string) *events.Message {
	pm := &waE2E.ProtocolMessage{Type: kind.Enum(), Key: &waCommon.MessageKey{ID: proto.String(target)}}
	if newText != "" {
		pm.EditedMessage = text(newText)
	}
	return msgEvt("EVT", chat, sender, fromMe, ingestT0, &waE2E.Message{ProtocolMessage: pm})
}

func contractHash(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func hashOptions(options []string) [][]byte {
	out := make([][]byte, len(options))
	for i, o := range options {
		out[i] = contractHash(o)
	}
	return out
}
