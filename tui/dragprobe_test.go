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

package tui_test

import (
	"fmt"
	"io"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/go-steer/core-tui/tui"
)

// ---------------------------------------------------------------
// Histogram
// ---------------------------------------------------------------

type dragHist struct{ samples []time.Duration }

func (h *dragHist) Add(d time.Duration) { h.samples = append(h.samples, d) }
func (h *dragHist) N() int              { return len(h.samples) }

func (h *dragHist) P(q float64) time.Duration {
	if len(h.samples) == 0 {
		return 0
	}
	s := slices.Clone(h.samples)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	idx := int(float64(len(s))*q/100 + 0.5)
	if idx < 1 {
		idx = 1
	}
	if idx > len(s) {
		idx = len(s)
	}
	return s[idx-1]
}

func (h *dragHist) Max() time.Duration {
	var m time.Duration
	for _, d := range h.samples {
		m = max(m, d)
	}
	return m
}

func (h *dragHist) Mean() time.Duration {
	if len(h.samples) == 0 {
		return 0
	}
	var t time.Duration
	for _, d := range h.samples {
		t += d
	}
	return t / time.Duration(len(h.samples))
}

func (h *dragHist) Summary() string {
	return fmt.Sprintf("n=%d mean=%s p50=%s p95=%s p99=%s max=%s",
		h.N(), rdur(h.Mean()), rdur(h.P(50)), rdur(h.P(95)), rdur(h.P(99)), rdur(h.Max()))
}

func rdur(d time.Duration) string {
	switch {
	case d >= time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
	case d >= time.Microsecond:
		return fmt.Sprintf("%.1fµs", float64(d)/float64(time.Microsecond))
	default:
		return d.String()
	}
}

// ---------------------------------------------------------------
// Corpus
// ---------------------------------------------------------------

var dragLangs = []string{"go", "python", "bash", "json"}

var dragFences = []string{
	"func handler(w http.ResponseWriter, r *http.Request) error {\n\tif r.Method != http.MethodPost {\n\t\treturn errMethod\n\t}\n\treturn json.NewEncoder(w).Encode(result)\n}",
	"def handler(request):\n    if request.method != \"POST\":\n        raise MethodError()\n    return json.dumps(result)",
	"set -euo pipefail\nfor f in \"$@\"; do\n  printf '%s\\n' \"$f\"\ndone",
	"{\n  \"name\": \"widget\",\n  \"count\": 42,\n  \"tags\": [\"a\", \"b\"]\n}",
}

func dragUserText(i int) string {
	return fmt.Sprintf("Turn %d: please review the handler and explain what it does, then suggest a fix.", i)
}

func dragAssistantText(i int) string {
	lang := dragLangs[i%len(dragLangs)]
	return fmt.Sprintf(`## Response %d

The handler validates the request method before decoding. That check is
**load-bearing**: without it a GET reaches the decoder and returns a
confusing 500 rather than a 405.

- it rejects non-POST early
- it encodes the result with the stdlib encoder
- it returns the encoder's error unwrapped

`+"```"+`%s
%s
`+"```"+`

That last point is worth a second look — an unwrapped error loses the
request context, so the log line downstream says nothing useful.`, i, lang, dragFences[i%len(dragFences)])
}

func dragCorpus(turns int) []tui.Message {
	out := make([]tui.Message, 0, turns*2)
	for i := range turns {
		out = append(out,
			tui.Message{Role: tui.RoleUser, Text: dragUserText(i)},
			tui.Message{Role: tui.RoleAssistant, Text: dragAssistantText(i)},
		)
	}
	return out
}

// ---------------------------------------------------------------
// Probe
// ---------------------------------------------------------------

type dragSample struct {
	at        time.Time
	got, want int
}

type dragState struct {
	mu sync.Mutex

	queue  []time.Time
	markAt time.Time
	fresh  bool

	wantW    []int
	views    int
	settling bool

	event, block, settleBlock, update dragHist
	samples                           []dragSample
}

type dragModel struct {
	inner tea.Model
	st    *dragState
}

func (p dragModel) Init() tea.Cmd { return p.inner.Init() }

func (p dragModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var enqueued time.Time
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		p.st.mu.Lock()
		if len(p.st.queue) > 0 {
			oldest := p.st.queue[0]
			p.st.queue = p.st.queue[1:]
			enqueued = oldest
			if !p.st.fresh {
				p.st.markAt = oldest
				p.st.fresh = true
			}
		}
		p.st.mu.Unlock()
	}
	next, cmd := p.inner.Update(msg)
	p.inner = next
	if !enqueued.IsZero() {
		p.st.mu.Lock()
		p.st.update.Add(time.Since(enqueued))
		p.st.mu.Unlock()
	}
	return p, cmd
}

func (p dragModel) View() tea.View {
	start := time.Now()
	v := p.inner.View()
	took := time.Since(start)

	p.st.mu.Lock()
	defer p.st.mu.Unlock()
	p.st.views++
	p.st.block.Add(took)
	if p.st.settling {
		p.st.settleBlock.Add(took)
	}
	if p.st.fresh {
		p.st.event.Add(time.Since(p.st.markAt))
		p.st.fresh = false
	}
	if n := len(p.st.wantW); n > 0 {
		p.st.samples = append(p.st.samples, dragSample{
			at:   time.Now(),
			got:  dragFrameWidth(v.Content),
			want: p.st.wantW[n-1],
		})
	}
	return v
}

func dragFrameWidth(s string) int {
	w := 0
	for ln := range strings.SplitSeq(s, "\n") {
		w = max(w, ansi.StringWidth(ln))
	}
	return w
}

type dragResult struct {
	event, block, settleBlock, update dragHist
	events, views                     int
	dragEnd                           time.Time
	finalWant                         int
	samples                           []dragSample
}

func (r dragResult) converged() (lag time.Duration, ok bool, overflow, underfill int) {
	for _, s := range r.samples {
		if s.at.Before(r.dragEnd) {
			continue
		}
		switch {
		case s.got > s.want:
			overflow++
		case s.got < s.want:
			underfill++
		}
	}
	for i := len(r.samples) - 1; i >= 0; i-- {
		s := r.samples[i]
		if s.at.Before(r.dragEnd) {
			break
		}
		if s.got != r.finalWant {
			break
		}
		lag, ok = s.at.Sub(r.dragEnd), true
	}
	return lag, ok, overflow, underfill
}

type dragConfig struct {
	events, everyMs int
	startW, height  int
	warmup, settle  time.Duration
}

func defaultDragConfig() dragConfig {
	return dragConfig{
		events: 40, everyMs: 30,
		startW: 120, height: 40,
		warmup: 1200 * time.Millisecond,
		settle: 1500 * time.Millisecond,
	}
}

func runDrag(t *testing.T, m tea.Model, cfg dragConfig) dragResult {
	t.Helper()
	st := &dragState{}
	p := tea.NewProgram(dragModel{inner: m, st: st},
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithoutSignals(),
		tea.WithoutSignalHandler(),
		tea.WithWindowSize(cfg.startW, cfg.height),
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := p.Run(); err != nil {
			t.Errorf("drag program: %v", err)
		}
	}()

	// The warmup has to outlast the startup reflow, not just Init:
	// sizing the program from 0 to startW is itself a width change, so
	// it arms the backlog warm, and a drag that starts on top of that
	// is measuring the warm as much as itself. The collection is for
	// the same reason — the corpus was allocated moments ago, and the
	// first GC of a fresh heap would otherwise land inside the drag.
	time.Sleep(cfg.warmup)
	runtime.GC()

	for i := range cfg.events {
		w := cfg.startW - 1 - (i % 40)
		st.mu.Lock()
		now := time.Now()
		st.queue = append(st.queue, now)
		st.wantW = append(st.wantW, w)
		st.mu.Unlock()
		p.Send(tea.WindowSizeMsg{Width: w, Height: cfg.height})
		time.Sleep(time.Duration(cfg.everyMs) * time.Millisecond)
	}

	st.mu.Lock()
	st.settling = true
	dragEnd := time.Now()
	finalWant := st.wantW[len(st.wantW)-1]
	st.mu.Unlock()

	time.Sleep(cfg.settle)
	p.Quit()
	<-done

	st.mu.Lock()
	defer st.mu.Unlock()
	return dragResult{
		event:       st.event,
		block:       st.block,
		settleBlock: st.settleBlock,
		update:      st.update,
		events:      len(st.wantW),
		views:       st.views,
		dragEnd:     dragEnd,
		finalWant:   finalWant,
		samples:     st.samples,
	}
}

// dragProbeGate is the per-frame budget a drag has to stay inside.
// One 60Hz frame: the operator does not read milliseconds, they read
// whether the pane edge stays under the pointer, and it stops doing
// that the moment a frame is missed.
const dragProbeGate = 16 * time.Millisecond

// dragProbeSizes are the transcript lengths the guard runs at. Two,
// not five: the claim #247 makes is that the per-event cost is FLAT
// in transcript length, and two points a hundred-fold apart test that
// for the price of two points. The full sweep (0/10/100/400/1000) is
// where the numbers in resize.go's header came from — restore it here
// when re-measuring, it costs about four seconds a size.
var dragProbeSizes = []int{10, 1000}

// dragProbeRaceDetector is set by the //go:build race half of this
// probe. See TestDragProbe_KeepsUpWithTheDrag for what it gates.
var dragProbeRaceDetector bool

// TestDragProbe_KeepsUpWithTheDrag is the acceptance measurement for
// issue #247, and the only test in the package that asserts on wall
// clock.
//
// It has to be wall clock. A debounce is a statement about time, and
// draining a Cmd tree synchronously — which is what every other test
// in resize_debounce_test.go does, correctly, for the work — measures
// the work and skips the wait. The question here is the other one:
// with events arriving every 30ms from a real terminal, does the
// frame the operator is looking at keep up? That needs a real
// tea.Program, a real clock, and events sent from outside the loop.
//
// Two accounting rules make the number honest:
//
//   - A frame is charged with the OLDEST event not yet on screen, not
//     the newest. If three events pile up behind one paint, the
//     operator is looking at a frame three events stale, and charging
//     it with the newest would report that as fast.
//   - The clock starts when the event is ENQUEUED, not when Update
//     receives it. Time spent in the Bubble Tea queue is time the
//     operator spends looking at the wrong frame.
//
// Under -race the timing assertion is skipped and only the
// correctness ones run. That is not a convenience: dev/tools/test-unit
// runs the whole suite with -race, and the detector inflates this
// path by ~14x (mean 6ms becomes mean 90ms), enough that events
// arrive faster than frames at every transcript length including
// zero. The number it would gate on is a fact about the detector, not
// about this package.
func TestDragProbe_KeepsUpWithTheDrag(t *testing.T) {
	if testing.Short() {
		t.Skip("the drag probe spends ~4s of wall clock per transcript size")
	}
	p95ByTurns := map[int]time.Duration{}
	for _, n := range dragProbeSizes {
		m := tui.NewModel(tui.Options{
			ForceTheme:  tui.ThemeDark,
			SeedHistory: dragCorpus(n),
		})
		r := runDrag(t, m, defaultDragConfig())
		lag, ok, overflow, underfill := r.converged()
		p95ByTurns[n] = r.event.P(95)

		t.Logf("turns=%d\n"+
			"  event enqueue->frame  %s\n"+
			"  event enqueue->update %s\n"+
			"  View() block (all)    %s\n"+
			"  View() block (settle) %s\n"+
			"  post-drag: overflow=%d underfill=%d converged=%v after %s (events=%d views=%d)",
			n, r.event.Summary(), r.update.Summary(), r.block.Summary(),
			r.settleBlock.Summary(), overflow, underfill, ok,
			lag.Round(time.Millisecond), r.events, r.views)

		// Correctness first, and unconditionally. Suppressing the
		// re-wrap mid-drag means the frame is drawn from rows wrapped
		// for a width the terminal no longer has; chatCutLine at the
		// draw site is what keeps that inside the frame, and overflow
		// is what would catch it failing to.
		if overflow != 0 {
			t.Errorf("turns=%d: %d frame(s) drew wider than the terminal during or after the drag", n, overflow)
		}
		// And the suppression has to be a deferral, not a drop: the
		// frame the drag comes to rest on is the one wrapped for the
		// width it came to rest at.
		if !ok {
			t.Errorf("turns=%d: the frame never converged on the final width after the drag ended", n)
		}

		if dragProbeRaceDetector {
			continue
		}
		if p95 := r.event.P(95); p95 > dragProbeGate {
			t.Errorf("turns=%d: enqueue-to-frame p95 is %s, over the %s frame budget (%s)",
				n, rdur(p95), rdur(dragProbeGate), r.event.Summary())
		}
	}

	if dragProbeRaceDetector || len(dragProbeSizes) < 2 {
		return
	}
	// Flatness, which is the claim the p95 gate alone does not test: a
	// per-event cost that grew with transcript length would pass at 10
	// turns and fail at some larger size nobody measured. The factor
	// is loose on purpose — this is a bound on the SHAPE of the curve,
	// and a tight one would just be a second, noisier copy of the gate
	// above. The floor keeps an unloaded machine's sub-millisecond
	// jitter from being read as growth.
	small, large := dragProbeSizes[0], dragProbeSizes[len(dragProbeSizes)-1]
	if p95ByTurns[small] > 2*time.Millisecond && p95ByTurns[large] > 3*p95ByTurns[small] {
		t.Errorf("per-event cost grows with transcript length: p95 is %s at %d turns and %s at %d",
			rdur(p95ByTurns[small]), small, rdur(p95ByTurns[large]), large)
	}
}
