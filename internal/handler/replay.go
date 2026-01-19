package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

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

	// Prepare the request to be replayed
	// We'll replay it to the same endpoint path on this server
	targetURL := "http://" + r.Host + "/h/" + reqData.EndpointID + reqData.Path

	client := &http.Client{}
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
				if k == "Host" || k == "Content-Length" || k == "Connection" {
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

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Replayed successfully. Response status: " + resp.Status))
}
