package opskat

import "encoding/json"

// EventWriter lets a tool emit real-time events (progress, partial results) to
// the page that called it. A caller that is not a page — AI exec, opsctl —
// ignores them, so a tool never has to know who called it.
type EventWriter struct{}

func newEventWriter() *EventWriter {
	return &EventWriter{}
}

// Send emits an event to the host, stamped by the host with this call's
// invocation id.
func (w *EventWriter) Send(eventType string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	hostToolEvent(eventType, payload)
	return nil
}
