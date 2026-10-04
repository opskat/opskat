package extension

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// The asset form asks the host to run the guest's validator before saving so the
// per-field errors reach the form's fields; only a loaded extension's own asset
// type may be validated.
func TestValidateExtensionConfigScope(t *testing.T) {
	Convey("ValidateExtensionConfig refuses what it cannot validate", t, func() {
		e, _ := newHostTestBinder(t)

		Convey("an extension that is not loaded", func() {
			_, err := e.ValidateExtensionConfig("ghost", "acme-store", `{}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not loaded")
		})
		Convey("an asset type the extension does not register", func() {
			_, err := e.ValidateExtensionConfig("acme", "other-store", `{}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "other-store")
		})
		Convey("an extension without a backend plugin", func() {
			_, err := e.ValidateExtensionConfig("acme", "acme-store", `{}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "no backend plugin")
		})
	})
}
