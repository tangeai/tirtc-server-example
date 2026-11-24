package cache

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"thing-connect/internal/call"
)

// The transition chunk is not runnable on its own; every free function it calls
// must come from the adapter prelude.
func TestCallRoomTransitionsUseOnlyPreludePrimitives(t *testing.T) {
	defined := func(src string) []string {
		var names []string
		for _, m := range regexp.MustCompile(`local function ([a-z_]+)\(`).FindAllStringSubmatch(src, -1) {
			names = append(names, m[1])
		}
		return names
	}
	local, prelude := defined(call.RoomTransitions), defined(callRoomPrelude)
	builtins := []string{"ipairs", "pairs", "not", "and", "or", "if", "function", "return", "send"}
	var code strings.Builder
	for _, line := range strings.Split(call.RoomTransitions, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			code.WriteString(line + "\n")
		}
	}
	missing := map[string]bool{}
	for _, m := range regexp.MustCompile(`(^|[^.\w])([a-z_]+)\(`).FindAllStringSubmatch(code.String(), -1) {
		name := m[2]
		if !slices.Contains(local, name) && !slices.Contains(builtins, name) && !slices.Contains(prelude, name) {
			missing[name] = true
		}
	}
	if len(missing) > 0 {
		t.Fatalf("room transitions call primitives missing from prelude: %v", missing)
	}
	for _, entry := range []string{"create_room", "answer_room", "end_room", "end_owned_room", "reject_room"} {
		if !slices.Contains(local, entry) {
			t.Fatalf("transition %s is not defined", entry)
		}
	}
}
