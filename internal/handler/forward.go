package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PipeOpsHQ/pipehook/internal/store"
)

func newForwardClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !allowPrivate && isPrivateOrReservedIP(ip) {
					continue
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			}
			return nil, errors.New("forward target resolves only to private or reserved addresses")
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many forwarding redirects")
		}
		return validateForwardURL(request.URL.String())
	}
	return client
}

func validateForwardURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return errors.New("forward URL must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("forward URL must use http or https")
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("forward URL must include a host and no credentials")
	}
	return nil
}

func isPrivateOrReservedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func (h *Handler) forwardRequest(ctx context.Context, endpoint *store.Endpoint, captured *store.Request) error {
	if endpoint.ForwardURL == "" {
		return nil
	}
	if err := validateForwardURL(endpoint.ForwardURL); err != nil {
		return err
	}

	target, _ := url.Parse(endpoint.ForwardURL)
	target.Path = strings.TrimSuffix(target.Path, "/") + replayRelativePath(captured)
	target.RawQuery = captured.QueryString
	request, err := http.NewRequestWithContext(ctx, captured.Method, target.String(), bytes.NewReader(captured.Body))
	if err != nil {
		return err
	}
	copyReplayHeaders(request.Header, captured.Headers)
	request.Header.Set("X-Pipehook-Forwarded", "true")

	response, err := h.ForwardClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 32*1024))
	return nil
}

func copyReplayHeaders(destination http.Header, rawHeaders string) {
	var headers map[string][]string
	if json.Unmarshal([]byte(rawHeaders), &headers) != nil {
		return
	}
	for key, values := range headers {
		switch strings.ToLower(key) {
		case "host", "content-length", "connection", "accept-encoding", "transfer-encoding":
			continue
		}
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func replayRelativePath(request *store.Request) string {
	prefix := "/h/" + request.EndpointID
	path := strings.TrimPrefix(request.Path, prefix)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func requestScheme(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		return forwarded
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func responseStatus(endpoint *store.Endpoint) int {
	if endpoint.DefaultStatus < 100 || endpoint.DefaultStatus > 599 {
		return http.StatusOK
	}
	return endpoint.DefaultStatus
}

func validateEndpointSettings(settings store.EndpointSettings) error {
	if settings.DefaultStatus < 200 || settings.DefaultStatus > 599 {
		return errors.New("response status must be between 200 and 599")
	}
	if settings.ResponseDelayMS < 0 || settings.ResponseDelayMS > store.MaxResponseDelayMS {
		return fmt.Errorf("response delay must be between 0 and %d milliseconds", store.MaxResponseDelayMS)
	}
	if settings.RequestLimit < 1 || settings.RequestLimit > store.MaxRequestLimit {
		return fmt.Errorf("request limit must be between 1 and %d", store.MaxRequestLimit)
	}
	if len(settings.DefaultBody) > 64*1024 {
		return errors.New("response body must not exceed 64KB")
	}
	if len(settings.Alias) > 120 || len(settings.DefaultContentType) > 200 || len(settings.ForwardURL) > 2048 {
		return errors.New("one or more settings exceed their maximum length")
	}
	return validateForwardURL(settings.ForwardURL)
}
