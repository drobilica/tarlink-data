package syncer

import (
	"strings"
	"testing"
)

func TestParseInstalledTarLinkJSON(t *testing.T) {
	apps, err := ParseInstalled(strings.NewReader(`[{"id":"example-app","installed_version":"1.0","unknown":true},{"id":"ignored","installed_version":""}]`))
	if err != nil || len(apps) != 1 || apps[0].ID != "example-app" {
		t.Fatalf("parse apps: %#v %v", apps, err)
	}
	for _, input := range []string{`[{"id":"a","installed_version":"1"}] trailing`, `not-json`} {
		if _, err := ParseInstalled(strings.NewReader(input)); err == nil {
			t.Errorf("expected malformed input rejection: %s", input)
		}
	}
}
