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

// Tests for the render-kick workaround (issue #24). The actual
// bubble-tea v2 scheduler quirk these handlers work around can't
// be deterministically reproduced in a unit test (it depends on
// goroutine scheduling around the program loop's idle state), so
// these are regression guards: each affected handler must return
// a non-nil Cmd whose eventual Msg is forceRenderMsg, and the
// forceRenderMsg handler itself must remain a no-op.

package tui

import (
	"errors"
	"testing"
)

func TestForceRenderMsg_NoOpHandler(t *testing.T) {
	// forceRenderMsg must NOT mutate model state — its only
	// purpose is to force a fresh Update → View cycle. If a
	// future refactor sneaks state into this handler, the modal-
	// painting story regresses.
	m := NewModel(Options{})
	m.viewport.SetWidth(80)
	historyBefore := m.history.Len()

	out, cmd := m.Update(forceRenderMsg{})
	got := out.(Model)

	if cmd != nil {
		t.Errorf("forceRenderMsg handler should return nil Cmd, got %T", cmd)
	}
	if got.history.Len() != historyBefore {
		t.Errorf("forceRenderMsg should not append history, got %d → %d", historyBefore, got.history.Len())
	}
	if got.pendingPermission != nil || got.pendingElicit != nil {
		t.Errorf("forceRenderMsg should not touch modal state")
	}
}

func TestPermissionRequestMsg_ReturnsRenderKickCmd(t *testing.T) {
	// Regression guard for issue #24: the permissionRequestMsg
	// handler used to return (m, nil), causing remote-delivered
	// prompts to stall until the next keypress. The fix returns
	// a forceRenderTick — verify by exercising the Cmd and
	// checking the eventual Msg type.
	m := NewModel(Options{})
	m.viewport.SetWidth(80)

	out, cmd := m.Update(permissionRequestMsg{
		req: PermissionRequest{ToolName: "bash", Detail: "ls /tmp"},
	})
	got := out.(Model)
	if got.pendingPermission == nil {
		t.Fatal("expected pendingPermission set")
	}
	if cmd == nil {
		t.Fatal("expected non-nil Cmd (render kick), got nil")
	}
	if _, ok := cmd().(forceRenderMsg); !ok {
		t.Errorf("expected forceRenderMsg from Cmd, got %T", cmd())
	}
}

func TestElicitRequestMsg_ReturnsRenderKickCmd(t *testing.T) {
	m := NewModel(Options{})
	m.viewport.SetWidth(80)

	out, cmd := m.Update(elicitRequestMsg{
		serverName: "test-mcp",
		req:        ElicitRequest{Title: "test", Description: "test"},
	})
	got := out.(Model)
	if got.pendingElicit == nil {
		t.Fatal("expected pendingElicit set")
	}
	if cmd == nil {
		t.Fatal("expected non-nil Cmd (render kick), got nil")
	}
	if _, ok := cmd().(forceRenderMsg); !ok {
		t.Errorf("expected forceRenderMsg from Cmd, got %T", cmd())
	}
}

func TestLiveStreamStartedMsg_ReturnsRenderKickCmd(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	_, cmd := m.Update(liveStreamStartedMsg{cancel: func() {}})
	if cmd == nil {
		t.Fatal("expected non-nil Cmd (render kick), got nil")
	}
	if _, ok := cmd().(forceRenderMsg); !ok {
		t.Errorf("expected forceRenderMsg from Cmd, got %T", cmd())
	}
}

func TestLiveStreamErrMsg_ReturnsRenderKickCmd(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	_, cmd := m.Update(liveStreamErrMsg{err: errors.New("boom")})
	if cmd == nil {
		t.Fatal("expected non-nil Cmd (render kick), got nil")
	}
	if _, ok := cmd().(forceRenderMsg); !ok {
		t.Errorf("expected forceRenderMsg from Cmd, got %T", cmd())
	}
}

func TestLiveStreamEndedMsg_ReturnsRenderKickCmd(t *testing.T) {
	m := NewModel(Options{Agent: newLiveAgentStub()})
	m.viewport.SetWidth(80)

	_, cmd := m.Update(liveStreamEndedMsg{})
	if cmd == nil {
		t.Fatal("expected non-nil Cmd (render kick), got nil")
	}
	if _, ok := cmd().(forceRenderMsg); !ok {
		t.Errorf("expected forceRenderMsg from Cmd, got %T", cmd())
	}
}
