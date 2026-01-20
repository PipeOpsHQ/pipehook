package handler

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/PipeOpsHQ/pipehook/internal/store"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) CaptureWebhook(w http.ResponseWriter, r *http.Request) {
	endpointID := chi.URLParam(r, "endpointID")
	if endpointID == "" {
		http.Error(w, "missing endpoint ID", http.StatusBadRequest)
		return
	}

	// Check if endpoint exists
	_, err := h.Store.GetEndpoint(r.Context(), endpointID)
	if err != nil {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}

	// Log request details BEFORE reading body
	contentLength := r.ContentLength
	contentType := r.Header.Get("Content-Type")
	transferEncoding := r.Header.Get("Transfer-Encoding")
	queryParams := r.URL.RawQuery

	// Detect Cloudflare and other proxies
	isCloudflare := r.Header.Get("Cf-Ray") != "" || r.Header.Get("Cdn-Loop") != ""
	hasProxyHeaders := r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-Ip") != ""

	log.Printf("=== REQUEST DEBUG ===")
	log.Printf("Method: %s, Path: %s, RemoteAddr: %s", r.Method, r.URL.Path, r.RemoteAddr)
	log.Printf("Content-Length header: %d", contentLength)
	log.Printf("Content-Type: %s", contentType)
	log.Printf("Transfer-Encoding: %s", transferEncoding)
	log.Printf("Query params: %s", queryParams)
	if isCloudflare {
		log.Printf("⚠️  Cloudflare detected (Cf-Ray: %s, Cdn-Loop: %s)", r.Header.Get("Cf-Ray"), r.Header.Get("Cdn-Loop"))
	}
	if hasProxyHeaders {
		log.Printf("⚠️  Proxy detected (X-Forwarded-For: %s, X-Real-Ip: %s)", r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Real-Ip"))
	}
	log.Printf("All headers: %+v", r.Header)

	// Check if body has been consumed and attempt restoration
	// Log body state before reading
	log.Printf("=== BODY STATE CHECK ===")
	log.Printf("Content-Length: %d", contentLength)
	log.Printf("Body is nil: %v", r.Body == nil)
	if r.GetBody != nil {
		log.Printf("GetBody() is available: true")
	} else {
		log.Printf("GetBody() is available: false")
	}

	// Read body - even if Content-Length is 0, we should still try to read
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body for %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	actualBodyLen := len(body)
	log.Printf("=== BODY DEBUG ===")
	log.Printf("Content-Length header: %d, Actual bytes read: %d", contentLength, actualBodyLen)
	log.Printf("Transfer-Encoding: %s", transferEncoding)

	// If body is empty but Content-Length indicates there should be data, try restoration
	if actualBodyLen == 0 && contentLength > 0 && r.GetBody != nil {
		log.Printf("⚠️  Body appears consumed (0 bytes read but Content-Length=%d). Attempting restoration via GetBody()...", contentLength)
		restoredBody, restoreErr := r.GetBody()
		if restoreErr == nil && restoredBody != nil {
			restoredData, readErr := io.ReadAll(restoredBody)
			restoredBody.Close()
			if readErr == nil && len(restoredData) > 0 {
				body = restoredData
				actualBodyLen = len(body)
				log.Printf("✅ Body restored successfully! Restored %d bytes (expected %d)", actualBodyLen, contentLength)
			} else if readErr != nil {
				log.Printf("⚠️  Failed to read from restored body: %v", readErr)
			} else {
				log.Printf("⚠️  Restored body is also empty")
			}
		} else {
			log.Printf("⚠️  Failed to get restored body: %v", restoreErr)
		}
	}

	if actualBodyLen > 0 {
		previewLen := actualBodyLen
		if previewLen > 500 {
			previewLen = 500
		}
		log.Printf("✅ Body captured successfully: %d bytes", actualBodyLen)

		// Try to format JSON preview nicely, otherwise show raw preview
		preview := body[:previewLen]
		var previewStr string
		if json.Valid(preview) {
			// It's valid JSON, try to format it
			var jsonObj interface{}
			if err := json.Unmarshal(preview, &jsonObj); err == nil {
				if formatted, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					previewStr = string(formatted)
					if len(previewStr) > 500 {
						previewStr = previewStr[:500] + "..."
					}
					log.Printf("Body preview (JSON, first %d bytes):\n%s", previewLen, previewStr)
				} else {
					previewStr = string(preview)
					log.Printf("Body preview (first %d bytes): %s", previewLen, previewStr)
				}
			} else {
				previewStr = string(preview)
				log.Printf("Body preview (first %d bytes): %s", previewLen, previewStr)
			}
		} else {
			// Not JSON, show raw preview (limit to safe printable characters)
			previewStr = string(preview)
			// Check if it's mostly printable
			printableCount := 0
			for _, b := range preview {
				if b >= 32 && b < 127 || b == '\n' || b == '\r' || b == '\t' {
					printableCount++
				}
			}
			if float64(printableCount)/float64(len(preview)) > 0.8 {
				log.Printf("Body preview (first %d bytes): %s", previewLen, previewStr)
			} else {
				log.Printf("Body preview (first %d bytes, binary): %d bytes of binary data", previewLen, len(preview))
			}
		}

		if contentLength > 0 && int64(actualBodyLen) != contentLength {
			log.Printf("⚠️  NOTE: Body length mismatch - Content-Length=%d, Actual=%d (difference: %d bytes)",
				contentLength, actualBodyLen, contentLength-int64(actualBodyLen))
		}
	} else if contentLength > 0 {
		// Content-Length says there should be a body, but we got nothing
		log.Printf("❌ CRITICAL: Content-Length=%d but captured 0 bytes!", contentLength)
		log.Printf("⚠️  Possible causes:")
		if isCloudflare {
			log.Printf("   - ⚠️  CLOUDFLARE MAY BE CONSUMING THE BODY")
			log.Printf("     Cloudflare WAF or proxy settings may be inspecting/stripping request bodies")
			log.Printf("     Check Cloudflare settings: Page Rules, WAF rules, or proxy mode settings")
		}
		log.Printf("   - Body consumed by middleware/proxy before handler")
		log.Printf("   - Body consumed by previous handler/middleware")
		log.Printf("   - Proxy/load balancer stripping body")
		log.Printf("   - Request body stream already closed")
		if r.GetBody == nil {
			log.Printf("   - GetBody() not available (cannot restore) - common with proxy/Cloudflare requests")
		}
	} else if transferEncoding == "chunked" {
		// Chunked encoding might not have Content-Length
		log.Printf("⚠️  Transfer-Encoding is chunked but body is empty - this is unusual")
		log.Printf("   Chunked encoding should have body data even without Content-Length header")
	} else {
		// Content-Length is 0, so empty body is expected
		log.Printf("ℹ️  Empty body (Content-Length=0, this is expected)")
	}

	// Check if body might be in query parameters (some proxies do this)
	if queryParams != "" && actualBodyLen == 0 {
		log.Printf("⚠️  NOTE: Query params present but body is empty: %s", queryParams)
	}

	// For form-urlencoded with empty body, check if it's in query params
	// (some systems send form data as query params when body is empty)
	if contentType == "application/x-www-form-urlencoded" && actualBodyLen == 0 && queryParams != "" {
		log.Printf("⚠️  Form-urlencoded with empty body but query params exist - body might be in query string")
		body = []byte(queryParams)
		actualBodyLen = len(body)
		log.Printf("⚠️  Using query params as body: %d bytes", actualBodyLen)
	}

	// Final body state summary
	log.Printf("=== FINAL BODY STATE ===")
	log.Printf("Content-Length header: %d", contentLength)
	log.Printf("Actual body length: %d bytes", actualBodyLen)
	log.Printf("Content-Type: %s", contentType)
	if actualBodyLen == 0 && contentLength > 0 {
		log.Printf("❌ BODY CAPTURE FAILED - Expected %d bytes but got 0", contentLength)
		if isCloudflare {
			log.Printf("⚠️  CLOUDFLARE DETECTED - This is likely a Cloudflare configuration issue")
			log.Printf("   Recommendation: Check Cloudflare proxy settings or disable proxy for this route")
		}
	} else if actualBodyLen == 0 && contentLength == 0 && isCloudflare {
		log.Printf("⚠️  NOTE: Empty body with Cloudflare - client may not be sending body, or Cloudflare is consuming it")
	}
	log.Printf("==================")

	// Handle compression if present
	contentEncoding := r.Header.Get("Content-Encoding")
	var decompressedBody []byte
	switch contentEncoding {
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			if db, err := io.ReadAll(gr); err == nil {
				decompressedBody = db
			}
			gr.Close()
		}
	case "deflate":
		zr, err := zlib.NewReader(bytes.NewReader(body))
		if err == nil {
			if db, err := io.ReadAll(zr); err == nil {
				decompressedBody = db
			}
			zr.Close()
		}
	}

	// If we successfully decompressed, use that for internal storage/display
	// but we might want to keep original body for binary integrity if replaying?
	// For now, let's store the decompressed version if it exists, otherwise the raw body.
	bodyToStore := body
	if len(decompressedBody) > 0 {
		bodyToStore = decompressedBody
	}

	// Capture all headers
	headersMap := make(map[string][]string)
	for k, v := range r.Header {
		headersMap[k] = v
	}
	headersJSON, _ := json.Marshal(headersMap)

	req := &store.Request{
		EndpointID: endpointID,
		Method:     r.Method,
		Path:       r.URL.Path,
		RemoteAddr: r.RemoteAddr,
		Headers:    string(headersJSON),
		Body:       bodyToStore,
		StatusCode: http.StatusOK,
	}

	if err := h.Store.SaveRequest(r.Context(), req); err != nil {
		log.Printf("Error saving request: %v", err)
		http.Error(w, "failed to save request", http.StatusInternalServerError)
		return
	}

	// Verify body was stored correctly by reading it back
	if req.ID > 0 {
		verifyReq, verifyErr := h.Store.GetRequest(r.Context(), req.ID)
		if verifyErr == nil && verifyReq != nil {
			storedBodyLen := len(verifyReq.Body)
			expectedBodyLen := len(bodyToStore)
			if storedBodyLen != expectedBodyLen {
				log.Printf("❌ DATABASE VERIFICATION FAILED: Expected %d bytes, stored %d bytes (difference: %d)",
					expectedBodyLen, storedBodyLen, expectedBodyLen-storedBodyLen)
			} else {
				log.Printf("✅ Database verification passed: %d bytes stored correctly", storedBodyLen)
			}
		} else {
			log.Printf("⚠️  Could not verify stored body: %v", verifyErr)
		}
	}

	h.Broadcast(endpointID, req)

	// Return success response
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
