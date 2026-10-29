package extension

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/pkg/extmanifest"

	. "github.com/smartystreets/goconvey/convey"
)

// The rules a describe() answer is judged by, and the typed view of what it
// contributes, belong to the host's contribution points (internal/extreg,
// snippet_svc) and are tested there. What stays here is the runtime's half:
// decoding the envelope and merging it, contributions raw, onto the manifest.

// baseDescriptor is one asset type and the policy face it is checked under.
const baseDescriptor = `"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
	`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
	`"policy":{"type":"x"}`

func TestApplyDescriptor(t *testing.T) {
	Convey("Merging describe() onto the manifest", t, func() {
		m, err := ParseManifest([]byte(`{"name":"oss","version":"1.0.0","hostABI":"1.0",` +
			`"backend":{"runtime":"wasm","binary":"main.wasm"},` +
			`"permissions":{"credentials":"read"}}`))
		So(err, ShouldBeNil)

		d, err := ParseDescriptor([]byte(`{"icon":"cloud-storage",` +
			`"i18n":{"displayName":"manifest.displayName","description":"manifest.description"},` +
			`"contributes":{` + baseDescriptor + `,"pages":{"entry":"frontend/index.js","pages":[{"id":"browser","slot":"asset.connect","i18n":{"name":"pages.browser.name"},"component":"BrowserPage"}]},` +
			`"tools":[` +
			`{"name":"list_objects","policyAction":"list","parameters":{"type":"object","properties":{}}},` +
			`{"name":"delete_object","policyAction":"delete","parameters":{"type":"object","properties":{}}},` +
			`{"name":"get_object","policyAction":"list","parameters":{"type":"object","properties":{}}}]}}`))
		So(err, ShouldBeNil)

		m.apply(d)

		Convey("the security contract is untouched", func() {
			So(m.Name, ShouldEqual, "oss")
			So(m.Permissions.Credentials, ShouldEqual, extmanifest.CredentialAccessRead)
			So(m.Backend.Binary, ShouldEqual, "main.wasm")
		})

		Convey("the presentation comes from the guest", func() {
			So(m.Icon, ShouldEqual, "cloud-storage")
			So(m.I18n.DisplayName, ShouldEqual, "manifest.displayName")
			So(m.I18n.Description, ShouldEqual, "manifest.description")
		})

		Convey("the contributions stay raw with it for the contribution points", func() {
			So(m.ContributionKeys(), ShouldResemble, []string{"assetTypes", "pages", "policy", "tools"})
			So(string(m.Contribution("policy")), ShouldEqual, `{"type":"x"}`)
			So(m.Contribution("snippets"), ShouldBeNil)
			f := faceOf(t, m)
			So(f.toolNames(), ShouldResemble, []string{"list_objects", "delete_object", "get_object"})
			So(f.AssetTypes, ShouldHaveLength, 1)

			v := newExtensionView(m, nil, nil, "", "")
			So(v.Keys(), ShouldResemble, m.ContributionKeys())
			So(string(v.Contribution("policy")), ShouldEqual, `{"type":"x"}`)
		})

		Convey("and the merged manifest a subsystem builds from the same parts is the same", func() {
			again := Described(m.Manifest, d)
			So(again, ShouldResemble, m)
		})
	})
}

// TestDescribeWireShape: describe() answers icon, i18n and contributes keyed by
// contribution point; the runtime reads the envelope and keeps each entry raw.
func TestDescribeWireShape(t *testing.T) {
	Convey("A describe() answer in the contributes shape", t, func() {
		payload := `{"icon":"cloud","i18n":{"displayName":"d","description":"x"},"contributes":{` +
			`"assetTypes":[{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
			`"tools":[{"name":"list","policyAction":"list","parameters":{"type":"object","properties":{}}}],` +
			`"unknownPoint":{"anything":true}}}`
		d, err := ParseDescriptor([]byte(payload))
		So(err, ShouldBeNil)
		m := &Manifest{}
		m.apply(d)

		So(m.Icon, ShouldEqual, "cloud")
		So(m.I18n.DisplayName, ShouldEqual, "d")
		// A key no point owns is the contribution registry's to refuse, not the envelope's.
		So(m.ContributionKeys(), ShouldResemble, []string{"assetTypes", "tools", "unknownPoint"})
		So(string(m.Contribution("unknownPoint")), ShouldEqual, `{"anything":true}`)

		Convey("and only the contract and presentation are the runtime manifest's JSON", func() {
			raw, err := json.Marshal(m)
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(raw, &out), ShouldBeNil)
			So(out, ShouldContainKey, "icon")
			So(out, ShouldContainKey, "i18n")
			So(out, ShouldNotContainKey, "assetTypes")
			So(out, ShouldNotContainKey, "tools")
		})
	})

	Convey("The flat pre-1.0 shape is refused rather than read as an empty describe", t, func() {
		_, err := ParseDescriptor([]byte(`{` + baseDescriptor + `}`))
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "assetTypes")
	})
}

const driverDescriptorJSON = `{"id":"mysql-ext","name":"MySQL Ext","icon":"x","order":50,"aliases":[],"defaultPort":3306,` +
	`"form":[{"tab":"connection","fields":["host","port"]}],"capabilities":["schemas"],` +
	`"createDatabaseOptions":[],"alterTableAttributes":[],"driverFields":[]}`

func TestDescribeIntoDatabaseDrivers(t *testing.T) {
	Convey("A cached describe() answer is merged without asking the guest", t, func() {
		useDescribeCache(t, newFakeDescribeCache(stubWasm, `{"icon":"ext-icon","contributes":{"databaseDrivers":[`+driverDescriptorJSON+`]}}`))
		m := &Manifest{Manifest: extmanifest.Manifest{
			Name:        "ext-a",
			HostABI:     extmanifest.HostABIVersion,
			Contributes: extmanifest.Contributes{DatabaseDrivers: []string{"mysql-ext"}},
		}}
		_, err := (&Manager{}).describeInto(context.Background(), m, nil, stubWasm)
		So(err, ShouldBeNil)
		So(m.Icon, ShouldEqual, "ext-icon")
		drivers := faceOf(t, m).DatabaseDrivers
		So(drivers, ShouldHaveLength, 1)
		So(string(drivers[0].ID), ShouldEqual, "mysql-ext")
	})
}
