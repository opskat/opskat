package extension

import (
	"os"
	"regexp"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestDecodePolicyDecision(t *testing.T) {
	Convey("a check_policy reply decodes into the action and the resources as globs", t, func() {
		Convey("the 2.0/2.1 single-resource reply names one literal resource", func() {
			action, resources, err := decodePolicyDecision([]byte(`{"action":"write","resource":"a*b"}`))
			So(err, ShouldBeNil)
			So(action, ShouldEqual, "write")
			So(resources, ShouldResemble, []string{`a\*b`})
		})

		Convey("an empty single resource is no resource", func() {
			_, resources, err := decodePolicyDecision([]byte(`{"action":"read","resource":""}`))
			So(err, ShouldBeNil)
			So(resources, ShouldBeEmpty)
		})

		Convey("a reply naming both shapes is refused rather than judged on one of them", func() {
			_, _, err := decodePolicyDecision([]byte(`{"action":"write","resource":"a","resources":["b"]}`))
			So(err, ShouldNotBeNil)
		})
	})
}

// HOST_UI_VERSION in the frontend is hand-kept in step with HostABIVersion (no
// generated Go→TS binding exists for a constant); two independent sources of the
// same contract version.
func TestHostUIVersionMirrorsHostABI(t *testing.T) {
	src, err := os.ReadFile("../../frontend/src/extension/inject.ts")
	if err != nil {
		t.Fatalf("read inject.ts: %v", err)
	}
	m := regexp.MustCompile(`export const HOST_UI_VERSION = "([^"]+)"`).FindSubmatch(src)
	if m == nil {
		t.Fatal("HOST_UI_VERSION not found in frontend/src/extension/inject.ts")
	}
	if got := string(m[1]); got != HostABIVersion {
		t.Fatalf("frontend HOST_UI_VERSION = %q, HostABIVersion = %q: bump both together", got, HostABIVersion)
	}
}
