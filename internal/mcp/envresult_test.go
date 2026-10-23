package mcp

import (
	"encoding/json"
	"testing"
)

// A key set to the empty string keeps its value, as the CLI door's answer
// does; only an absent key omits it (api §5).
func TestAnEmptyServerEnvValueIsKept(t *testing.T) {
	empty := ""
	set, err := json.Marshal(envResult{Key: "K", Present: true, Value: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if string(set) != `{"key":"K","present":true,"value":""}` {
		t.Fatalf("a set, empty key answered %s", set)
	}
	absent, err := json.Marshal(envResult{Key: "K"})
	if err != nil {
		t.Fatal(err)
	}
	if string(absent) != `{"key":"K","present":false}` {
		t.Fatalf("an absent key answered %s", absent)
	}
}
