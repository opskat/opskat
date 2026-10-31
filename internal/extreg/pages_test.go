package extreg

import (
	"os"
	"regexp"
	"slices"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
)

func pagesDesc(entry, styles, pages string) []byte {
	return describeJSON(`"pages":{"entry":"` + entry + `","styles":"` + styles + `","pages":[` + pages + `]}`)
}

const connectPage = `{"id":"main","slot":"asset.connect","i18n":{"name":"n"},"component":"Main"}`

func TestPageRules(t *testing.T) {
	Convey("Page declarations", t, func() {
		Convey("a page in the asset.connect slot with paths inside the directory loads", func() {
			So(check(t, pagesDesc("frontend/index.js", "frontend/a.css", connectPage)), ShouldBeNil)
		})

		Convey("an extension without styles loads", func() {
			So(check(t, pagesDesc("index.js", "", connectPage)), ShouldBeNil)
		})

		Convey("a page without a slot is refused, naming the page", func() {
			err := check(t, pagesDesc("index.js", "", `{"id":"orphan","i18n":{"name":"n"},"component":"C"}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "orphan")
			So(err.Error(), ShouldContainSubstring, "slot")
		})

		Convey("a page in an unknown slot is refused, naming the page and the slot", func() {
			err := check(t, pagesDesc("index.js", "", `{"id":"p","slot":"sidebar","i18n":{"name":"n"},"component":"C"}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `"p"`)
			So(err.Error(), ShouldContainSubstring, "sidebar")
		})

		Convey("entry and styles paths outside the extension directory are refused", func() {
			for _, c := range []struct{ entry, styles string }{
				{"../x.js", ""},
				{"a/../../x.js", ""},
				{"/abs/x.js", ""},
				{`\\abs\\x.js`, ""},
				{"https://evil.test/x.js", ""},
				{"index.js", "../x.css"},
				{"index.js", "http://evil.test/x.css"},
			} {
				err := check(t, pagesDesc(c.entry, c.styles, connectPage))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldStartWith, "ext: pages:")
			}
		})
	})
}

// PageSlots and the frontend's PAGE_SLOTS are written separately; this compares
// the two so a slot added on one side only fails here.
func TestPageSlotsMatchFrontend(t *testing.T) {
	src, err := os.ReadFile("../../frontend/src/extension/slots.ts")
	require.NoError(t, err)
	m := regexp.MustCompile(`PAGE_SLOTS\s*=\s*\[([^\]]*)\]`).FindSubmatch(src)
	require.NotNil(t, m, "PAGE_SLOTS array not found in slots.ts")
	quoted := regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(m[1], -1)
	ts := make([]string, 0, len(quoted))
	for _, q := range quoted {
		ts = append(ts, string(q[1]))
	}
	got := slices.Clone(PageSlots)
	slices.Sort(got)
	slices.Sort(ts)
	require.Equal(t, got, ts)
}
