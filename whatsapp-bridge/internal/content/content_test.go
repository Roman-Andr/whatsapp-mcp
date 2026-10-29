package content

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// Captions were a silent hole: a message was stored with an empty content
// column, which looks exactly like an image sent without a single word. This
// test exists so that difference cannot quietly disappear again.
func TestTextReadsCaptions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"nil", nil, ""},
		{
			"conversation",
			&waE2E.Message{Conversation: new("hello")},
			"hello",
		},
		{
			"extended text",
			&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: new("hello")}},
			"hello",
		},
		{
			"image with caption",
			&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: new("look at this")}},
			"look at this",
		},
		{
			"image without caption",
			&waE2E.Message{ImageMessage: &waE2E.ImageMessage{}},
			"",
		},
		{
			"multiline caption is preserved",
			&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: new("first\nsecond\n\nfourth")}},
			"first\nsecond\n\nfourth",
		},
		{
			"video with caption",
			&waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: new("clip")}},
			"clip",
		},
		{
			"document with caption",
			&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: new("the contract")}},
			"the contract",
		},
		{
			// How a captioned document actually arrives over history sync.
			"document-with-caption envelope",
			&waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
				Message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
					Caption: new("the contract"),
				}},
			}},
			"the contract",
		},
		{
			"nested envelopes: ephemeral around view-once",
			&waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{
				Message: &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{
					Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
						Caption: new("disappearing"),
					}},
				}},
			}},
			"disappearing",
		},
		{
			// Audio has no caption field in the protocol; nothing to invent.
			"audio",
			&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}},
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Text(tc.msg); got != tc.want {
				t.Errorf("Text() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStructuredMessagesBecomeText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: new(55.75), DegreesLongitude: new(37.62), Name: new("Office")}}, "[location] 55.750000,37.620000 Office"},
		{"contact", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: new("Bob")}}, "[contact] Bob"},
		{"poll", &waE2E.Message{PollCreationMessageV3: &waE2E.PollCreationMessage{Name: new("Lunch?"), Options: []*waE2E.PollCreationMessage_Option{{OptionName: new("yes")}, {OptionName: new("no")}}}}, "[poll] Lunch?: yes / no"},
		{"button reply", &waE2E.Message{ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{Response: &waE2E.ButtonsResponseMessage_SelectedDisplayText{SelectedDisplayText: "Confirm"}}}, "Confirm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Text(c.msg); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Business senders were silently dropped: their text sits in template and
// interactive payloads the extractor did not read.
func TestBusinessPayloadsBecomeText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{
			"hydrated template with buttons",
			&waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{
				HydratedTemplate: &waE2E.TemplateMessage_HydratedFourRowTemplate{
					HydratedContentText: new("Your parcel 123 arrives today"),
					HydratedFooterText:  new("DHL"),
					HydratedButtons: []*waE2E.HydratedTemplateButton{{
						HydratedButton: &waE2E.HydratedTemplateButton_UrlButton{UrlButton: &waE2E.HydratedTemplateButton_HydratedURLButton{
							DisplayText: new("Track"), URL: new("https://dhl.example/123"),
						}},
					}},
				},
			}},
			"Your parcel 123 arrives today\nDHL\n[Track https://dhl.example/123]",
		},
		{
			"interactive with copy-code button",
			&waE2E.Message{InteractiveMessage: &waE2E.InteractiveMessage{
				Body: &waE2E.InteractiveMessage_Body{Text: new("Your code is 482913")},
				InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
					Buttons: []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
						{Name: new("cta_copy"), ButtonParamsJSON: new(`{"display_text":"Copy","copy_code":"482913"}`)},
					},
				}},
			}},
			"Your code is 482913\n[Copy 482913]",
		},
		{
			"device sent envelope",
			&waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{Message: &waE2E.Message{Conversation: new("from my phone")}}},
			"from my phone",
		},
		{
			"four-row template fallback params",
			&waE2E.Message{TemplateMessage: &waE2E.TemplateMessage{
				Format: &waE2E.TemplateMessage_FourRowTemplate_{FourRowTemplate: &waE2E.TemplateMessage_FourRowTemplate{
					Content: &waE2E.HighlyStructuredMessage{Params: []string{"Payment", "of", "$5"}},
				}},
			}},
			"Payment of $5",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Text(c.msg); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestContextInfoAndQuotedPreview(t *testing.T) {
	t.Parallel()
	t.Run("reply with mentions quoting an uncaptioned image", func(t *testing.T) {
		t.Parallel()
		msg := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: new("see above"),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID:      new("Q1"),
				Participant:   new("111@lid"),
				MentionedJID:  []string{"222@lid"},
				QuotedMessage: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}},
			},
		}}
		ci := ContextInfo(msg)
		if ci.GetStanzaID() != "Q1" || ci.GetParticipant() != "111@lid" || len(ci.GetMentionedJID()) != 1 {
			t.Errorf("context info not read: %v", ci)
		}
		if got := QuotedPreview(ci.GetQuotedMessage()); got != "[image]" {
			t.Errorf("quoted preview of uncaptioned image = %q", got)
		}
	})
	t.Run("reply on an image", func(t *testing.T) {
		t.Parallel()
		img := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{ContextInfo: &waE2E.ContextInfo{StanzaID: new("Q2")}}}
		if ContextInfo(img).GetStanzaID() != "Q2" {
			t.Error("reply on an image not detected")
		}
	})
}
