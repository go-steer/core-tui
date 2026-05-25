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

// `@<path>` file-reference inlining (R-PAL-2 + R-CHAT-13). When the
// operator submits a prompt containing `@some/file.go` tokens, the
// referenced files are read and appended to the prompt under a
// "Referenced files:" section so the model sees the file contents
// inline rather than just the path.
//
// Lifted from internal/tui/files.go so the on-wire prompt shape
// stays identical across both hosts.

package tui

import (
	"os"
	"strings"
)

// maxAtRefBytes caps a single @-ref read at 64 KiB. A 10 MB log
// referenced casually would otherwise blow up the prompt + context
// window. The truncation appends "... [truncated]" so the agent
// knows it didn't get everything.
const maxAtRefBytes = 64 * 1024

// expandAtRefs scans prompt for `@<path>` tokens (preceded by
// whitespace or at start of string), reads each referenced file via
// fileReader, and returns the original prompt followed by an
// appended "Referenced files" section.
//
// Files that fail to read are noted in the returned diagnostics
// slice so the caller can surface them to the user; the expansion
// still succeeds (with those refs omitted from the appended
// section) so the prompt round-trips even when some refs are bad.
//
// Dedup by path — referencing the same file twice in one prompt
// inlines it once.
func expandAtRefs(prompt string, fileReader func(string) ([]byte, error)) (expanded string, refs []string, diagnostics []string) {
	tokens := tokenizeAtRefs(prompt)
	if len(tokens) == 0 {
		return prompt, nil, nil
	}
	var b strings.Builder
	b.WriteString(prompt)
	wroteHeader := false
	seen := map[string]bool{}
	for _, t := range tokens {
		if seen[t] {
			continue
		}
		seen[t] = true
		data, err := fileReader(t)
		if err != nil {
			diagnostics = append(diagnostics, "could not read "+t+": "+err.Error())
			continue
		}
		if !wroteHeader {
			b.WriteString("\n\nReferenced files:\n")
			wroteHeader = true
		}
		refs = append(refs, t)
		b.WriteString("\n--- ")
		b.WriteString(t)
		b.WriteString(" ---\n")
		b.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	if !wroteHeader {
		// All refs failed; return the original prompt with diagnostics.
		return prompt, nil, diagnostics
	}
	return b.String(), refs, diagnostics
}

// tokenizeAtRefs returns the file references found in s. A reference
// is an `@<path>` token where `<path>` is a non-empty run of
// non-whitespace characters and the `@` is at start of string or
// preceded by whitespace.
func tokenizeAtRefs(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		c := s[i]
		if c != '@' || (i > 0 && !isWhitespaceByte(s[i-1])) {
			i++
			continue
		}
		j := i + 1
		for j < len(s) && !isWhitespaceByte(s[j]) {
			j++
		}
		path := s[i+1 : j]
		if path != "" {
			out = append(out, path)
		}
		i = j
	}
	return out
}

func isWhitespaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// readFileSafe reads via os.ReadFile and tail-truncates at maxBytes
// so a giant log referenced casually doesn't blow up the prompt.
func readFileSafe(maxBytes int) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if maxBytes > 0 && len(data) > maxBytes {
			data = data[:maxBytes]
			data = append(data, []byte("\n... [truncated]\n")...)
		}
		return data, nil
	}
}
