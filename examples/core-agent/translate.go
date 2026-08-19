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

package main

import (
	"github.com/go-steer/core-tui/examples/core-agent/fakehost"
	"github.com/go-steer/core-tui/tui"
)

// translateEvent flattens one host event onto tui.Event. Both
// flavors share it: the local agent yields these values directly,
// the attach client decodes them off an SSE frame, and from
// core-tui's side the two are indistinguishable — which is the
// whole claim I-CORE-AGENT makes.
//
// The mapping is the least mechanical part of any adapter, because
// the host's tree (Event → Content → []Part → call / response /
// text) has to collapse onto core-tui's flat struct.
func translateEvent(ev *fakehost.Event, model string) tui.Event {
	te := tui.Event{Partial: ev.Partial, Model: model}
	if u := ev.UsageMetadata; u != nil {
		te.Usage = &tui.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
	}
	if ev.Content == nil {
		return te
	}
	for _, p := range ev.Content.Parts {
		switch {
		case p.FunctionCall != nil:
			te.ToolCalls = append(te.ToolCalls, tui.ToolCall{
				ID:   p.FunctionCall.ID,
				Name: p.FunctionCall.Name,
				Args: p.FunctionCall.Args,
			})
		case p.FunctionResponse != nil:
			response, errText := splitFunctionResponse(p.FunctionResponse)
			te.ToolResults = append(te.ToolResults, tui.ToolResult{
				ID:       p.FunctionResponse.ID,
				Name:     p.FunctionResponse.Name,
				Response: response,
				Error:    errText,
				// LatencyMs stays zero on purpose: core-tui plucks it
				// out of Response["latency_ms"], which is already
				// where the host puts it.
			})
		default:
			te.Text += p.Text
		}
	}
	return te
}

// translatePause maps a gate transition onto the one tui.Event field
// core-tui watches for it. It is a whole Event with nothing else set,
// which is exactly right: a pause is not something the agent SAID, so
// it must not carry text into the transcript — core-tui writes its
// own system row off the event.
func translatePause(pe *fakehost.PauseEvent) tui.Event {
	return tui.Event{Pause: &tui.PauseEvent{
		State:       pe.State,
		Reason:      pe.Reason,
		Interrupted: pe.Interrupted,
		Mode:        pe.Mode,
		At:          pe.At,
	}}
}

// translateSubagentTurns maps the host's recorded background-agent
// turns onto the page tui.SubagentReporter.SubagentEvents returns.
// Both flavors share it — local reads the turns in process, attach
// decodes them off a JSON body, and the shape core-tui sees is the
// same either way.
func translateSubagentTurns(turns []fakehost.SubagentTurn) []tui.SubagentEvent {
	out := make([]tui.SubagentEvent, 0, len(turns))
	for _, t := range turns {
		ev := tui.SubagentEvent{Seq: t.Seq, Timestamp: t.At, Author: t.Author, Text: t.Text}
		for _, c := range t.Calls {
			ev.ToolCalls = append(ev.ToolCalls, tui.SubagentToolCall{ID: c.ID, Name: c.Name, Args: c.Args})
		}
		for _, r := range t.Results {
			response, errText := splitFunctionResponse(&r)
			ev.ToolResults = append(ev.ToolResults, tui.SubagentToolResult{
				ID: r.ID, Name: r.Name, Response: response, Error: errText,
			})
		}
		out = append(out, ev)
	}
	return out
}

// splitFunctionResponse lifts the host's in-band "error" key out of
// the response map, because core-tui models tool failure as its own
// field. Exactly the kind of glue design.md §6.0 says belongs on the
// host's side of the seam.
func splitFunctionResponse(fr *fakehost.FunctionResponse) (map[string]any, string) {
	if fr.Response == nil {
		return nil, ""
	}
	msg, ok := fr.Response["error"].(string)
	if !ok {
		return fr.Response, ""
	}
	rest := make(map[string]any, len(fr.Response)-1)
	for k, v := range fr.Response {
		if k != "error" {
			rest[k] = v
		}
	}
	return rest, msg
}
