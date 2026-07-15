package handler

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) ReplayRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "requestID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid request ID", http.StatusBadRequest)
		return
	}
	captured, ok := h.requireRequestAccess(w, r, id)
	if !ok {
		return
	}

	target := url.URL{
		Scheme: requestScheme(r), Host: r.Host, Path: "/h/" + captured.EndpointID + replayRelativePath(captured),
		RawQuery: captured.QueryString,
	}
	replay, err := http.NewRequestWithContext(r.Context(), captured.Method, target.String(), bytes.NewReader(captured.Body))
	if err != nil {
		http.Error(w, "failed to create replay request", http.StatusInternalServerError)
		return
	}
	copyReplayHeaders(replay.Header, captured.Headers)

	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(replay)
	if err != nil {
		http.Error(w, "failed to replay request", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32*1024))
	w.Header().Set("HX-Trigger", "requestReplayed")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "Replayed successfully (Status: %s)", response.Status)
}
