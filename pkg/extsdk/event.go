package opskat

import "encoding/json"

// EventWriter allows action handlers to emit real-time events.
type EventWriter struct{}

func newEventWriter() *EventWriter {
	return &EventWriter{}
}

// Send emits an event to the host. The host forwards it to the frontend.
func (w *EventWriter) Send(eventType string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	sendActionEvent(eventType, payload)
	return nil
}

// sendActionEvent calls the host function. Implemented by hostcall_*.go files.
func sendActionEvent(eventType string, data []byte) {
	hostActionEvent(eventType, data)
}
