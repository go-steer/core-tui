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
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// drain runs a turn to its end and reports how many events it yielded
// and how it stopped.
func drain(t *testing.T, a *Agent, prompt string) (int, error) {
	t.Helper()
	n := 0
	for ev, err := range a.Run(t.Context(), prompt) {
		if err != nil {
			return n, err
		}
		_ = ev
		n++
	}
	return n, nil
}

// TestInterruptStopsTheRunningTurn is the property the whole hold path
// leans on and that nothing checked: Pause shuts the gate against the
// NEXT turn, so stopping the one already running is Interrupt's job
// alone (issue #280).
func TestInterruptStopsTheRunningTurn(t *testing.T) {
	a := NewAgent("")

	started := make(chan struct{})
	var once sync.Once
	var n int
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev, e := range a.Run(t.Context(), "check the api deployment") {
			if e != nil {
				err = e
				return
			}
			_ = ev
			n++
			once.Do(func() { close(started) })
		}
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never produced an event")
	}
	if !a.Interrupt() {
		t.Fatal("Interrupt reported no turn in flight while one was streaming")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn ran on after being interrupted")
	}

	if err == nil {
		t.Fatal("the interrupted turn ended cleanly, as if nothing had happened")
	}
	if !strings.Contains(err.Error(), "interrupted by operator") {
		t.Errorf("turn ended with %v, want the operator interrupt", err)
	}
	full, _ := drain(t, NewAgent(""), "check the api deployment")
	if n >= full {
		t.Errorf("the interrupted turn yielded %d of %d events — it was not cut short", n, full)
	}
	if got := a.State(); got != "idle" {
		t.Errorf("State() = %q after the interrupted turn ended, want idle", got)
	}
}

// TestInterruptIsNotSwallowedByAConcurrentTurn. running and
// interrupted used to be single booleans shared by every turn, and
// beginTurn cleared the flag — so a turn starting between an Interrupt
// and the interrupted turn's next check took the interrupt with it and
// both ran to completion.
//
// Concurrent turns are ordinary here rather than exotic: the TUI's
// per-turn subscription and an injected steer each start one, which is
// exactly the arrangement the operator was in when esc left a turn
// running.
func TestInterruptIsNotSwallowedByAConcurrentTurn(t *testing.T) {
	a := NewAgent("")

	first := make(chan struct{})
	var once sync.Once
	var firstErr error
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		for _, e := range a.Run(t.Context(), "check the api deployment") {
			if e != nil {
				firstErr = e
				return
			}
			once.Do(func() { close(first) })
		}
	}()

	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never produced an event")
	}
	a.Interrupt()

	// The interloper: started after the interrupt, so it is work the
	// operator never asked to stop and it must run to completion —
	// while taking nothing away from the turn they did stop.
	secondDone := make(chan struct{})
	var secondErr error
	var secondN int
	go func() {
		defer close(secondDone)
		secondN, secondErr = drain(t, a, "and the worker pool?")
	}()

	select {
	case <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the interrupted turn ran on; a concurrent turn swallowed its interrupt")
	}
	if firstErr == nil {
		t.Error("the interrupted turn ended cleanly")
	}

	select {
	case <-secondDone:
	case <-time.After(20 * time.Second):
		t.Fatal("the turn started after the interrupt never finished")
	}
	if secondErr != nil {
		t.Errorf("the turn started after the interrupt was cut short by %v; the operator never asked to stop it", secondErr)
	}
	if secondN == 0 {
		t.Error("the turn started after the interrupt yielded nothing")
	}
}

// TestInterruptOnAnIdleAgentReportsNothingKilled. The bit matters: it
// is what makes the TUI's banner say "Agent held" rather than
// "Interrupted —", i.e. whether the operator's work died.
func TestInterruptOnAnIdleAgentReportsNothingKilled(t *testing.T) {
	a := NewAgent("")
	if a.Interrupt() {
		t.Error("Interrupt on an idle agent claimed to have cancelled a turn")
	}
	if got := a.State(); got != "idle" {
		t.Errorf("State() = %q, want idle", got)
	}
	// And a turn afterwards is unaffected — the interrupt did not
	// leave a flag behind for the next turn to trip over.
	if _, err := drain(t, a, "check the api deployment"); err != nil {
		t.Errorf("the next turn ended with %v, want a clean run", err)
	}
}

// TestStateReportsARunningTurn keeps State honest now that it counts
// turns rather than reading a flag: the daemon's status endpoint and
// the interrupt's "was anything killed" answer both come from it.
func TestStateReportsARunningTurn(t *testing.T) {
	a := NewAgent("")
	if got := a.State(); got != "idle" {
		t.Fatalf("State() = %q on a fresh agent, want idle", got)
	}

	ctx, cancel := context.WithCancel(t.Context())
	running := make(chan struct{})
	var once sync.Once
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range a.Run(ctx, "check the api deployment") {
			once.Do(func() { close(running) })
		}
	}()
	select {
	case <-running:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never produced an event")
	}
	if got := a.State(); got != "running" {
		t.Errorf("State() = %q with a turn in flight, want running", got)
	}

	cancel()
	<-done
	if got := a.State(); got != "idle" {
		t.Errorf("State() = %q after the turn ended, want idle", got)
	}
}
