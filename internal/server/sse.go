package server

import (
	"fmt"
	"net/http"
)

// sseWriter writes a server-sent event stream. Every write is flushed, so a
// result reaches the client when it is found, not when the buffer fills.
type sseWriter struct {
	w          http.ResponseWriter
	controller *http.ResponseController
}

// startSSE sends the headers at once, so the client knows the check began.
// nginx buffers a response unless told not to, which would deliver the results
// in bursts.
func startSSE(w http.ResponseWriter) (*sseWriter, error) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := &sseWriter{w: w, controller: http.NewResponseController(w)}
	return stream, stream.controller.Flush()
}

// event writes one event; data is sent as compact JSON.
func (s *sseWriter) event(name string, data any) error {
	encoded, err := marshalJSON(data, "")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, encoded); err != nil {
		return err
	}
	return s.controller.Flush()
}

// keepAlive writes a comment, which clients ignore, so that a proxy does not
// close a stream that is quiet while a slow site answers.
func (s *sseWriter) keepAlive() error {
	if _, err := fmt.Fprint(s.w, ":\n\n"); err != nil {
		return err
	}
	return s.controller.Flush()
}
