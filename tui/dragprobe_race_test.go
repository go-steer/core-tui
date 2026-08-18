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

//go:build race

package tui_test

// The race detector is on, so the drag probe's wall-clock assertion
// is off. dev/tools/test-unit runs the whole suite with -race, which
// makes this the CI configuration rather than the exotic one; see
// TestDragProbe_KeepsUpWithTheDrag for why a number measured under
// the detector is not a number about this package.
func init() { dragProbeRaceDetector = true }
