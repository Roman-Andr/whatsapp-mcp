package content

import (
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// EventText renders a WhatsApp event (a meeting with a date, created in a
// group) as one line: "[event] Name, 2026-11-01 18:00 +03:00 – 20:00 at …".
func EventText(e *waE2E.EventMessage) string {
	var b strings.Builder
	b.WriteString("[event] ")
	if e.GetIsCanceled() {
		b.WriteString("(cancelled) ")
	}
	b.WriteString(e.GetName())
	if start := e.GetStartTime(); start > 0 {
		st := time.Unix(start, 0)
		b.WriteString(", " + st.Format("2006-01-02 15:04 -07:00"))
		if end := e.GetEndTime(); end > start {
			et := time.Unix(end, 0)
			if et.Format("2006-01-02") == st.Format("2006-01-02") {
				b.WriteString(" – " + et.Format("15:04"))
			} else {
				b.WriteString(" – " + et.Format("2006-01-02 15:04"))
			}
		}
	}
	if loc := e.GetLocation(); loc != nil {
		where := strings.TrimSpace(loc.GetName() + " " + loc.GetAddress())
		if where == "" && (loc.GetDegreesLatitude() != 0 || loc.GetDegreesLongitude() != 0) {
			where = fmt.Sprintf("%.6f,%.6f", loc.GetDegreesLatitude(), loc.GetDegreesLongitude())
		}
		if where != "" {
			b.WriteString(" at " + where)
		}
	}
	if link := e.GetJoinLink(); link != "" {
		b.WriteString(" " + link)
	}
	if d := strings.TrimSpace(e.GetDescription()); d != "" {
		b.WriteString("\n" + d)
	}
	return b.String()
}

var eventResponseNames = map[waE2E.EventResponseMessage_EventResponseType]string{
	waE2E.EventResponseMessage_GOING:     "going",
	waE2E.EventResponseMessage_NOT_GOING: "not going",
	waE2E.EventResponseMessage_MAYBE:     "maybe",
}

// EventResponseName is the stored name of an answer to an event: "going",
// "not going" or "maybe". ok is false for an unknown answer.
func EventResponseName(t waE2E.EventResponseMessage_EventResponseType) (name string, ok bool) {
	name, ok = eventResponseNames[t]
	return name, ok
}
