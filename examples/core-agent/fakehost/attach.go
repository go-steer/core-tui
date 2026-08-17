// Copyright 2026 The go-steer team
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fakehost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file fakes the two halves of core-agent's attach mode: the
// daemon that serves a session over HTTP + SSE, and the
// `internal/attachclient.Client` an operator's TUI points at it.
//
// The daemon is real HTTP on loopback, not a mock — the attach
// flavor of this example genuinely does its streaming over a socket.
// What is fake is the agent behind it (the same canned turn as the
// local flavor) and the API's breadth: core-agent's attach surface
// has ~30 endpoints, this has seven.

// Frame is one sequenced event off the SSE stream. Seq is the
// resume cursor: a client that drops re-subscribes with ?since=Seq
// and the daemon replays from there.
type Frame struct {
	Seq   int64  `json:"seq"`
	Event *Event `json:"event"`
}

// StatusInfo is the daemon's answer to GET {session}/status.
type StatusInfo struct {
	Model    string `json:"model"`
	State    string `json:"state"`
	Provider string `json:"provider"`
}

// UsageInfo is the daemon's answer to GET {session}/usage.
type UsageInfo struct {
	Session      TurnUsage `json:"session"`
	LastTurn     TurnUsage `json:"last_turn"`
	Turns        int       `json:"turns"`
	WindowSize   int       `json:"window_size"`
	WindowUsed   int       `json:"window_used"`
	UptimeMillis int64     `json:"uptime_ms"`
}

// SessionDescriptor is one row from GET /sessions — what an attach
// client enumerates before it picks something to attach to.
type SessionDescriptor struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Note  string `json:"note"`
}

// Daemon serves one Agent over the attach API.
type Daemon struct {
	agent *Agent
	srv   *http.Server
	url   string

	mu      sync.Mutex
	seq     int64
	history []Frame
	subs    map[chan Frame]struct{}
}

// SessionPath is the path prefix every per-session endpoint hangs
// off, matching core-agent's /sessions/{app}/{id} layout.
const SessionPath = "/sessions/core-agent/demo-session"

// StartDaemon binds a loopback listener and serves agent on it until
// the returned Daemon is closed.
func StartDaemon(agent *Agent) (*Daemon, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	d := &Daemon{
		agent: agent,
		url:   "http://" + ln.Addr().String(),
		subs:  map[chan Frame]struct{}{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions", d.handleSessions)
	mux.HandleFunc("GET "+SessionPath+"/events", d.handleEvents)
	mux.HandleFunc("GET "+SessionPath+"/tools", d.handleTools)
	mux.HandleFunc("GET "+SessionPath+"/agents", d.handleAgents)
	mux.HandleFunc("GET "+SessionPath+"/agents/{name}/events", d.handleSubagentEvents)
	mux.HandleFunc("GET "+SessionPath+"/status", d.handleStatus)
	mux.HandleFunc("GET "+SessionPath+"/usage", d.handleUsage)
	mux.HandleFunc("POST "+SessionPath+"/inject", d.handleInject)
	mux.HandleFunc("POST "+SessionPath+"/interrupt", d.handleInterrupt)
	mux.HandleFunc("POST "+SessionPath+"/slash/btw", d.handleBtw)
	d.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = d.srv.Serve(ln) }()
	return d, nil
}

// URL is the daemon's base URL.
func (d *Daemon) URL() string { return d.url }

// Close shuts the daemon down and releases every subscriber.
func (d *Daemon) Close() error {
	d.mu.Lock()
	for ch := range d.subs {
		close(ch)
		delete(d.subs, ch)
	}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return d.srv.Shutdown(ctx)
}

func (d *Daemon) publish(ev *Event) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	f := Frame{Seq: d.seq, Event: ev}
	d.history = append(d.history, f)
	for ch := range d.subs {
		select {
		case ch <- f:
		default: // slow subscriber: drop rather than stall the turn
		}
	}
}

func (d *Daemon) subscribe(since int64) (chan Frame, []Frame) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := make(chan Frame, 64)
	d.subs[ch] = struct{}{}
	var replay []Frame
	for _, f := range d.history {
		if f.Seq > since {
			replay = append(replay, f)
		}
	}
	return ch, replay
}

func (d *Daemon) unsubscribe(ch chan Frame) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.subs[ch]; ok {
		delete(d.subs, ch)
		close(ch)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (d *Daemon) handleSessions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, []SessionDescriptor{
		{Path: SessionPath, Label: "demo-session", Note: "the session this example serves"},
		{Path: "/sessions/core-agent/incident-4471", Label: "incident-4471", Note: "another session on the same daemon"},
	})
}

func (d *Daemon) handleTools(w http.ResponseWriter, _ *http.Request) { writeJSON(w, d.agent.Tools()) }

func (d *Daemon) handleAgents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, d.agent.Subagents())
}

// SubagentEventsResponse is the daemon's answer to
// GET {session}/agents/{name}/events. Available is populated only on
// the 404 path.
type SubagentEventsResponse struct {
	Events    []SubagentTurn `json:"events"`
	NextSince int64          `json:"next_since"`
	Truncated bool           `json:"truncated"`
	Error     string         `json:"error,omitempty"`
	Available []string       `json:"available,omitempty"`
}

func (d *Daemon) handleSubagentEvents(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	turns, err := d.agent.SubagentTurns(r.PathValue("name"), since)
	var unknown *UnknownSubagentError
	if errors.As(err, &unknown) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, SubagentEventsResponse{Error: unknown.Error(), Available: unknown.Available})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var next int64
	if n := len(turns); n > 0 {
		next = turns[n-1].Seq
	} else {
		next = since
	}
	writeJSON(w, SubagentEventsResponse{Events: turns, NextSince: next})
}

func (d *Daemon) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, StatusInfo{
		Model:    d.agent.ModelName(),
		State:    d.agent.State(),
		Provider: d.agent.Provider(),
	})
}

func (d *Daemon) handleUsage(w http.ResponseWriter, _ *http.Request) {
	size, used := d.agent.ContextWindow()
	writeJSON(w, UsageInfo{
		Session:      d.agent.SessionUsage(),
		LastTurn:     d.agent.LastTurnUsage(),
		Turns:        d.agent.Turns(),
		WindowSize:   size,
		WindowUsed:   used,
		UptimeMillis: d.agent.Uptime().Milliseconds(),
	})
}

func (d *Daemon) handleInterrupt(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]bool{"cancelled": d.agent.Interrupt()})
}

func (d *Daemon) handleBtw(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := d.agent.AskSideQuestion(r.Context(), body.Question)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"answer": answer})
}

// handleInject is what makes attach mode "push": the operator's
// prompt is a fire-and-forget POST, and the answer comes back on
// the SSE stream everyone attached to the session is watching.
func (d *Daemon) handleInject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	go func() {
		for ev, err := range d.agent.Run(context.Background(), body.Message) {
			if err != nil {
				d.publish(&Event{
					Author:       "system",
					Content:      &Content{Parts: []Part{{Text: err.Error()}}},
					TurnComplete: true,
				})
				return
			}
			if ev.UsageMetadata != nil {
				// In attach mode the DAEMON owns the accounting; the
				// operator's TUI reads a projection of it over
				// GET {session}/usage.
				d.agent.AppendUsage(*ev.UsageMetadata)
			}
			d.publish(ev)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

func (d *Daemon) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	ch, replay := d.subscribe(since)
	defer d.unsubscribe(ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	send := func(f Frame) bool {
		payload, err := json.Marshal(f)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for _, f := range replay {
		if !send(f) {
			return
		}
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case f, open := <-ch:
			if !open || !send(f) {
				return
			}
		}
	}
}

// Client is the fake of core-agent's `internal/attachclient.Client`
// — short-lived HTTP requests for the RPCs, one long-lived SSE
// subscription for the event stream.
type Client struct {
	base string
	hc   *http.Client
}

// NewClient points a client at a daemon's base URL.
func NewClient(baseURL string) *Client {
	return &Client{
		base: strings.TrimSuffix(baseURL, "/"),
		// No overall timeout: Stream holds its connection open for
		// the life of the session. Per-call deadlines ride on ctx.
		hc: &http.Client{},
	}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: status %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Sessions enumerates the daemon's sessions.
func (c *Client) Sessions(ctx context.Context) ([]SessionDescriptor, error) {
	var out []SessionDescriptor
	return out, c.get(ctx, "/sessions", &out)
}

// Tools lists the remote agent's tools.
func (c *Client) Tools(ctx context.Context, sessionPath string) ([]Tool, error) {
	var out []Tool
	return out, c.get(ctx, sessionPath+"/tools", &out)
}

// Agents lists the remote agent's background subagents.
func (c *Client) Agents(ctx context.Context, sessionPath string) ([]Subagent, error) {
	var out []Subagent
	return out, c.get(ctx, sessionPath+"/agents", &out)
}

// SubagentEvents pages a background agent's recorded turns. A name
// the daemon can't resolve comes back as an *UnknownSubagentError,
// preserving the "no such subagent" vs "did nothing" distinction
// across the wire.
func (c *Client) SubagentEvents(ctx context.Context, sessionPath, name string, since int64) (SubagentEventsResponse, error) {
	url := fmt.Sprintf("%s%s/agents/%s/events?since=%d", c.base, sessionPath, name, since)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return SubagentEventsResponse{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return SubagentEventsResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out SubagentEventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SubagentEventsResponse{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return out, &UnknownSubagentError{Name: name, Available: out.Available}
	}
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("subagent events: status %d", resp.StatusCode)
	}
	return out, nil
}

// Status polls the remote agent's model + run state.
func (c *Client) Status(ctx context.Context, sessionPath string) (StatusInfo, error) {
	var out StatusInfo
	return out, c.get(ctx, sessionPath+"/status", &out)
}

// Usage polls the remote agent's accounting.
func (c *Client) Usage(ctx context.Context, sessionPath string) (UsageInfo, error) {
	var out UsageInfo
	return out, c.get(ctx, sessionPath+"/usage", &out)
}

// Inject posts an operator message into the remote turn.
func (c *Client) Inject(ctx context.Context, sessionPath, message string) error {
	return c.post(ctx, sessionPath+"/inject", map[string]string{"message": message}, nil)
}

// Interrupt asks the daemon to cancel the in-flight turn.
func (c *Client) Interrupt(ctx context.Context, sessionPath string) (bool, error) {
	var out struct {
		Cancelled bool `json:"cancelled"`
	}
	return out.Cancelled, c.post(ctx, sessionPath+"/interrupt", nil, &out)
}

// Btw forwards a side question to the remote agent.
func (c *Client) Btw(ctx context.Context, sessionPath, question string) (string, error) {
	var out struct {
		Answer string `json:"answer"`
	}
	err := c.post(ctx, sessionPath+"/slash/btw", map[string]string{"question": question}, &out)
	return out.Answer, err
}

// Stream subscribes to the session's SSE event stream, resuming
// after sequence `since`. The returned channel closes when the
// stream ends (clean turn end, transport drop, or ctx cancel) —
// telling the two apart is the adapter's job, which is why
// attach.go loops with backoff around it.
func (c *Client) Stream(ctx context.Context, sessionPath string, since int64) (<-chan Frame, error) {
	url := fmt.Sprintf("%s%s/events?since=%d", c.base, sessionPath, since)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("stream: status %d", resp.StatusCode)
	}
	out := make(chan Frame, 16)
	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var f Frame
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
				continue
			}
			select {
			case out <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
