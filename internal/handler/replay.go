package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) ReplayRequest(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "requestID")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	reqData, err := h.Store.GetRequest(r.Context(), id)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}

	// Determine the protocol (default to http, but check for https)
	proto := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		proto = "https"
	}

	// Prepare the request to be replayed
	targetURL := fmt.Sprintf("%s://%s/h/%s%s", proto, r.Host, reqData.EndpointID, reqData.Path)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	newReq, err := http.NewRequest(reqData.Method, targetURL, bytes.NewReader(reqData.Body))
	if err != nil {
		http.Error(w, "failed to create replay request", http.StatusInternalServerError)
		return
	}

	// Restore headers
	var headers map[string][]string
	if err := json.Unmarshal([]byte(reqData.Headers), &headers); err == nil {
		for k, v := range headers {
			for _, val := range v {
				// Don't replay certain headers that should be unique to the new request
				kLower := strings.ToLower(k)
				if kLower == "host" || kLower == "content-length" || kLower == "connection" || kLower == "accept-encoding" {
					continue
				}
				newReq.Header.Add(k, val)
			}
		}
	}

	resp, err := client.Do(newReq)
	if err != nil {
		http.Error(w, "failed to replay request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("HX-Trigger", "requestReplayed")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Replayed successfully (Status: %s)", resp.Status)
}
