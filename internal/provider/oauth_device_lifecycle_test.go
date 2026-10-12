package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/arthur-sommer-etc/cliproxyapi-copilot-plugin/internal/transport"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type oauthDeviceLifecycleHost struct {
	mu sync.Mutex

	deviceRequests int
	tokenRequests  int
	userRequests   int
	tokenResponses []transport.Response
	userResponses  []transport.Response
	userErrors     []error
	deviceEntered  chan struct{}
	deviceRelease  <-chan struct{}
}

func (h *oauthDeviceLifecycleHost) Do(ctx context.Context, _ string, request transport.Request) (transport.Response, error) {
	switch request.URL {
	case "https://github.com/login/device/code":
		h.mu.Lock()
		h.deviceRequests++
		entered := h.deviceEntered
		release := h.deviceRelease
		h.mu.Unlock()
		if entered != nil {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return transport.Response{}, ctx.Err()
			}
		}
		return transport.Response{StatusCode: http.StatusOK, Body: []byte(`{"device_code":"synthetic-device-code","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":300,"interval":5}`)}, nil
	case "https://github.com/login/oauth/access_token":
		h.mu.Lock()
		defer h.mu.Unlock()
		h.tokenRequests++
		if len(h.tokenResponses) > 0 {
			response := h.tokenResponses[0]
			h.tokenResponses = h.tokenResponses[1:]
			return response, nil
		}
		return transport.Response{StatusCode: http.StatusOK, Body: []byte(`{"access_token":"synthetic-access-token","refresh_token":"synthetic-refresh-token","token_type":"bearer","scope":"read:user","expires_in":3600,"refresh_token_expires_in":7200}`)}, nil
	case "https://api.github.com/user":
		h.mu.Lock()
		defer h.mu.Unlock()
		h.userRequests++
		if len(h.userErrors) > 0 {
			err := h.userErrors[0]
			h.userErrors = h.userErrors[1:]
			if err != nil {
				return transport.Response{}, err
			}
		}
		if len(h.userResponses) > 0 {
			response := h.userResponses[0]
			h.userResponses = h.userResponses[1:]
			return response, nil
		}
		return transport.Response{StatusCode: http.StatusOK, Body: []byte(`{"login":"synthetic-user","id":4242}`)}, nil
	default:
		return transport.Response{}, fmt.Errorf("unexpected synthetic request %s %s", request.Method, request.URL)
	}
}

func (h *oauthDeviceLifecycleHost) OpenStream(context.Context, string, transport.Request) (transport.Stream, error) {
	return transport.Stream{}, errors.New("unexpected synthetic stream request")
}

func (h *oauthDeviceLifecycleHost) ReadStream(context.Context, string) (transport.StreamChunk, error) {
	return transport.StreamChunk{}, errors.New("unexpected synthetic stream read")
}

func (h *oauthDeviceLifecycleHost) CloseStream(context.Context, string) error { return nil }

func (h *oauthDeviceLifecycleHost) Emit(context.Context, string, []byte) error { return nil }

func (h *oauthDeviceLifecycleHost) CloseOutput(context.Context, string, string) {}

func (h *oauthDeviceLifecycleHost) counts() (device, token, user int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deviceRequests, h.tokenRequests, h.userRequests
}

func TestPollLoginRetainsTokenUntilAccountVerificationRecovers(t *testing.T) {
	issuedAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	now := issuedAt
	var clockMu sync.Mutex
	host := &oauthDeviceLifecycleHost{userResponses: []transport.Response{
		{StatusCode: http.StatusServiceUnavailable, Body: []byte(`{"message":"synthetic temporary failure"}`)},
		{StatusCode: http.StatusOK, Body: []byte(`{"login":"synthetic-user","id":4242}`)},
	}}
	service := New(host)
	service.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	start, err := service.StartLogin(context.Background(), "synthetic-callback")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}

	first, err := service.PollLogin(context.Background(), "synthetic-callback", start.State)
	if err != nil || first.Status != pluginapi.AuthLoginStatusPending || first.Auth.StorageJSON != nil {
		t.Fatalf("poll after transient verification failure = %#v, error=%v", first, err)
	}
	service.oauthMu.Lock()
	pending := service.oauthSession[start.State]
	if pending == nil || pending.PendingToken == nil || !pending.TokenIssuedAt.Equal(issuedAt) {
		service.oauthMu.Unlock()
		t.Fatal("successful token exchange was not retained with its original issuance time")
	}
	service.oauthMu.Unlock()

	tooSoon, err := service.PollLogin(context.Background(), "synthetic-callback", start.State)
	if err != nil || tooSoon.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("poll before interval = %#v, error=%v", tooSoon, err)
	}
	_, tokenRequests, userRequests := host.counts()
	if tokenRequests != 1 || userRequests != 1 {
		t.Fatalf("early poll repeated upstream work: token=%d user=%d", tokenRequests, userRequests)
	}

	clockMu.Lock()
	now = issuedAt.Add(5 * time.Second)
	clockMu.Unlock()
	recovered, err := service.PollLogin(context.Background(), "synthetic-callback", start.State)
	if err != nil || recovered.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("verification retry = %#v, error=%v", recovered, err)
	}
	storage, err := parseStorage(recovered.Auth.StorageJSON)
	if err != nil {
		t.Fatalf("parse verified credentials: %v", err)
	}
	if storage.GitHubLogin != "synthetic-user" || storage.GitHubUserID != 4242 {
		t.Fatalf("verified identity = %q/%d", storage.GitHubLogin, storage.GitHubUserID)
	}
	if want := issuedAt.Add(time.Hour).Unix(); storage.ExpiresAt != want {
		t.Fatalf("access token expiry = %d, want original issuance expiry %d", storage.ExpiresAt, want)
	}
	if want := issuedAt.Add(2 * time.Hour).Unix(); storage.RefreshTokenExpiresAt != want {
		t.Fatalf("refresh token expiry = %d, want original issuance expiry %d", storage.RefreshTokenExpiresAt, want)
	}
	if storage.UpdatedAt != issuedAt.Format(time.RFC3339) {
		t.Fatalf("credential timestamp = %q, want issuance timestamp %q", storage.UpdatedAt, issuedAt.Format(time.RFC3339))
	}
	if _, tokenRequests, userRequests = host.counts(); tokenRequests != 1 || userRequests != 2 {
		t.Fatalf("verification retry exchanged device token again: token=%d user=%d", tokenRequests, userRequests)
	}
	service.oauthMu.Lock()
	_, retained := service.oauthSession[start.State]
	service.oauthMu.Unlock()
	if retained {
		t.Fatal("successful verification retained the device session")
	}
}

func TestPollLoginExpiresPendingTokenAndClearsSession(t *testing.T) {
	issuedAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	now := issuedAt
	var clockMu sync.Mutex
	host := &oauthDeviceLifecycleHost{
		tokenResponses: []transport.Response{{StatusCode: http.StatusOK, Body: []byte(`{"access_token":"synthetic-access-token","refresh_token":"synthetic-refresh-token","expires_in":4}`)}},
		userErrors:     []error{errors.New("synthetic temporary network failure")},
	}
	service := New(host)
	service.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	start, err := service.StartLogin(context.Background(), "synthetic-callback")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	first, err := service.PollLogin(context.Background(), "synthetic-callback", start.State)
	if err != nil || first.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("first poll = %#v, error=%v", first, err)
	}
	clockMu.Lock()
	now = issuedAt.Add(5 * time.Second)
	clockMu.Unlock()
	expired, err := service.PollLogin(context.Background(), "synthetic-callback", start.State)
	if err != nil || expired.Status != pluginapi.AuthLoginStatusError {
		t.Fatalf("poll with expired access token = %#v, error=%v", expired, err)
	}
	service.oauthMu.Lock()
	_, retained := service.oauthSession[start.State]
	service.oauthMu.Unlock()
	if retained {
		t.Fatal("expired access token retained its device session")
	}
	_, tokenRequests, userRequests := host.counts()
	if tokenRequests != 1 || userRequests != 1 {
		t.Fatalf("expired token caused another upstream request: token=%d user=%d", tokenRequests, userRequests)
	}
}

func TestPollLoginExpiresDeviceSessionAndShutdownClearsPendingToken(t *testing.T) {
	now := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	var clockMu sync.Mutex
	host := &oauthDeviceLifecycleHost{userErrors: []error{errors.New("synthetic temporary network failure")}}
	service := New(host)
	service.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	start, err := service.StartLogin(context.Background(), "synthetic-callback")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	first, err := service.PollLogin(context.Background(), "synthetic-callback", start.State)
	if err != nil || first.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("first poll = %#v, error=%v", first, err)
	}
	service.Shutdown()
	service.oauthMu.Lock()
	remaining := len(service.oauthSession)
	service.oauthMu.Unlock()
	if remaining != 0 {
		t.Fatalf("Shutdown retained %d OAuth sessions", remaining)
	}

	second, err := service.StartLogin(context.Background(), "synthetic-callback")
	if err != nil {
		t.Fatalf("second StartLogin: %v", err)
	}
	clockMu.Lock()
	now = second.ExpiresAt
	clockMu.Unlock()
	expired, err := service.PollLogin(context.Background(), "synthetic-callback", second.State)
	if err != nil || expired.Status != pluginapi.AuthLoginStatusError {
		t.Fatalf("poll after device expiry = %#v, error=%v", expired, err)
	}
	service.oauthMu.Lock()
	_, retained := service.oauthSession[second.State]
	service.oauthMu.Unlock()
	if retained {
		t.Fatal("expired device session was not cleared")
	}
}

func TestStartLoginPrunesExpiredSessionsWithoutEvictingActiveSessions(t *testing.T) {
	now := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	host := &oauthDeviceLifecycleHost{}
	service := New(host)
	service.now = func() time.Time { return now }
	active := &deviceSession{DeviceCode: "synthetic-active", ExpiresAt: now.Add(time.Minute)}
	service.oauthMu.Lock()
	service.oauthSession["synthetic-active-state"] = active
	for index := 0; index < maxOAuthSessions-1; index++ {
		service.oauthSession[fmt.Sprintf("synthetic-expired-state-%d", index)] = &deviceSession{ExpiresAt: now.Add(-time.Second)}
	}
	service.oauthMu.Unlock()

	start, err := service.StartLogin(context.Background(), "synthetic-callback")
	if err != nil {
		t.Fatalf("StartLogin after pruning: %v", err)
	}
	service.oauthMu.Lock()
	defer service.oauthMu.Unlock()
	if service.oauthSession["synthetic-active-state"] != active {
		t.Fatal("pruning evicted an active OAuth session")
	}
	if got := len(service.oauthSession); got != 2 {
		t.Fatalf("OAuth session count after pruning and start = %d, want 2", got)
	}
	if service.oauthSession[start.State] == nil {
		t.Fatal("new OAuth session was not retained")
	}
}

func TestStartLoginBoundsConcurrentSessionsAndReturns429WhenFull(t *testing.T) {
	release := make(chan struct{})
	host := &oauthDeviceLifecycleHost{deviceEntered: make(chan struct{}, maxOAuthSessions), deviceRelease: release}
	service := New(host)
	responses := make(chan struct {
		start pluginapi.AuthLoginStartResponse
		err   error
	}, maxOAuthSessions+1)
	for index := 0; index < maxOAuthSessions+1; index++ {
		go func() {
			start, err := service.StartLogin(context.Background(), "synthetic-callback")
			responses <- struct {
				start pluginapi.AuthLoginStartResponse
				err   error
			}{start: start, err: err}
		}()
	}

	for index := 0; index < maxOAuthSessions; index++ {
		select {
		case <-host.deviceEntered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("concurrent StartLogin calls did not fill the session bound")
		}
	}
	var saturated error
	select {
	case result := <-responses:
		saturated = result.err
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("saturated StartLogin did not return promptly")
	}
	var status *StatusError
	if !errors.As(saturated, &status) || status.HTTPStatus != http.StatusTooManyRequests || status.Message != "Too many active GitHub device login sessions" {
		close(release)
		t.Fatalf("saturated StartLogin error = %v, want static HTTP 429", saturated)
	}
	service.oauthMu.Lock()
	if got := len(service.oauthSession); got != maxOAuthSessions {
		service.oauthMu.Unlock()
		close(release)
		t.Fatalf("reserved OAuth sessions = %d, want %d", got, maxOAuthSessions)
	}
	existing := make(map[string]*deviceSession, len(service.oauthSession))
	for state, session := range service.oauthSession {
		existing[state] = session
	}
	service.oauthMu.Unlock()

	close(release)
	for index := 0; index < maxOAuthSessions; index++ {
		select {
		case result := <-responses:
			if result.err != nil || result.start.State == "" {
				t.Fatalf("accepted concurrent StartLogin failed: %#v, error=%v", result.start, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("accepted concurrent StartLogin did not finish")
		}
	}
	service.oauthMu.Lock()
	defer service.oauthMu.Unlock()
	if len(service.oauthSession) != maxOAuthSessions {
		t.Fatalf("completed OAuth sessions = %d, want %d", len(service.oauthSession), maxOAuthSessions)
	}
	for state, session := range existing {
		if service.oauthSession[state] != session {
			t.Fatal("concurrent saturation evicted an accepted session")
		}
	}
}
