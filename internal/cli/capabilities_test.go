package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// Every capability reaches the human output: verbs' help sends a reader to
// `olympus capabilities` for spawn_sizing, spawn_command and session_status,
// and a list that leaves one out says the backend lacks it. Checked against
// the struct itself, so a new capability cannot be forgotten.
func TestHumanCapabilitiesNameEveryCapability(t *testing.T) {
	var all backend.Capabilities
	v := reflect.ValueOf(&all).Elem()
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() == reflect.Bool {
			v.Field(i).SetBool(true)
		}
	}
	listed := strings.Split(describeCapabilities(all), ", ")
	bools := 0
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() == reflect.Bool {
			bools++
		}
	}
	if len(listed) != bools {
		t.Errorf("the human output names %d capabilities, the struct has %d: %v", len(listed), bools, listed)
	}
}

// The agents help names every agent the detection tables know, so it cannot
// fall behind them.
func TestTheAgentsHelpNamesEveryKind(t *testing.T) {
	long := (&App{}).agentsCmd().Long
	for _, kind := range olympus.Kinds() {
		if !strings.Contains(long, kind.Name) {
			t.Errorf("the agents help does not name %s", kind.Name)
		}
	}
}
