package syncer

import (
	"strings"
	"testing"
)

func TestParseInstalledTarLinkJSON(t *testing.T) {
	apps, err := ParseInstalled(strings.NewReader(`{"version":1,"applications":[{"id":"example-app","version":"1.0"}]}`))
	if err != nil || len(apps) != 1 || apps[0].ID != "example-app" {
		t.Fatalf("parse apps: %#v %v", apps, err)
	}
	for _, input := range []string{
		`{"version":2,"applications":[]}`,
		`{"version":1}`,
		`{"version":1,"applications":null}`,
		`{"version":1,"applications":[],"unknown":true}`,
		`{"version":1,"applications":[{"id":"a","version":"1"},{"id":"a","version":"2"}]}`,
		`{"version":1,"applications":[{"id":"","version":"1"}]}`,
		`{"version":1,"applications":[{"id":"a","version":""}]}`,
		`{"version":1,"applications":[]} trailing`,
		`not-json`,
	} {
		if _, err := ParseInstalled(strings.NewReader(input)); err == nil {
			t.Errorf("expected malformed input rejection: %s", input)
		}
	}
}

func TestParseInstalledRejectsOversizedInput(t *testing.T) {
	if _, err := ParseInstalled(strings.NewReader(strings.Repeat(" ", maxInputBytes+1))); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized input error = %v", err)
	}
}
