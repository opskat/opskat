package main

import (
	"encoding/json"
	"testing"

	opskat "github.com/opskat/opskat/pkg/extsdk"

	. "github.com/smartystreets/goconvey/convey"
)

// The tools are exercised through TestHost, which dispatches the same way the
// WASM entry point does: the registrations in init() are what these tests reach,
// and the host KV / asset config they depend on are the SDK's own fakes.

const testAsset = int64(7)

func newHost(cfg notebookConfig) *opskat.TestHost {
	return opskat.NewTestHost(opskat.WithAssetConfig(testAsset, cfg))
}

// decode re-decodes a tool result into a typed value; TestHost hands back the
// generic JSON shape the host would receive.
func decode(t *testing.T, result any, out any) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
}

func TestNotebookTools(t *testing.T) {
	Convey("notebook tools", t, func() {
		host := newHost(notebookConfig{Notebook: "team-runbooks"})
		defer host.Close()

		Convey("a stored note is listed and read back", func() {
			_, err := host.CallTool("note_put", putArgs{
				AssetID: testAsset,
				Key:     "runbook/failover",
				Content: "1. drain 2. promote",
				Tags:    []string{"runbook", "db"},
			})
			So(err, ShouldBeNil)

			listed, err := host.CallTool("note_list", listArgs{AssetID: testAsset})
			So(err, ShouldBeNil)
			var list struct {
				Notebook string        `json:"notebook"`
				Count    int           `json:"count"`
				Notes    []noteSummary `json:"notes"`
			}
			decode(t, listed, &list)
			So(list.Notebook, ShouldEqual, "team-runbooks")
			So(list.Count, ShouldEqual, 1)
			So(list.Notes[0].Key, ShouldEqual, "runbook/failover")
			So(list.Notes[0].Tags, ShouldResemble, []string{"runbook", "db"})
			So(list.Notes[0].Size, ShouldEqual, len("1. drain 2. promote"))
			So(list.Notes[0].UpdatedAt, ShouldNotBeEmpty)

			got, err := host.CallTool("note_get", getArgs{AssetID: testAsset, Key: "runbook/failover"})
			So(err, ShouldBeNil)
			var n note
			decode(t, got, &n)
			So(n.Content, ShouldEqual, "1. drain 2. promote")
		})

		Convey("writing an existing key overwrites it instead of adding one", func() {
			first, err := host.CallTool("note_put", putArgs{AssetID: testAsset, Key: "scratch", Content: "a"})
			So(err, ShouldBeNil)
			var created struct {
				Created bool `json:"created"`
				Count   int  `json:"count"`
			}
			decode(t, first, &created)
			So(created.Created, ShouldBeTrue)

			second, err := host.CallTool("note_put", putArgs{AssetID: testAsset, Key: "scratch", Content: "b"})
			So(err, ShouldBeNil)
			var overwritten struct {
				Created bool `json:"created"`
				Count   int  `json:"count"`
			}
			decode(t, second, &overwritten)
			So(overwritten.Created, ShouldBeFalse)
			So(overwritten.Count, ShouldEqual, 1)

			got, err := host.CallTool("note_get", getArgs{AssetID: testAsset, Key: "scratch"})
			So(err, ShouldBeNil)
			var n note
			decode(t, got, &n)
			So(n.Content, ShouldEqual, "b")
		})

		Convey("listing filters by key prefix", func() {
			for _, key := range []string{"runbook/failover", "runbook/restore", "incident/2026-03-11"} {
				_, err := host.CallTool("note_put", putArgs{AssetID: testAsset, Key: key, Content: "x"})
				So(err, ShouldBeNil)
			}

			listed, err := host.CallTool("note_list", listArgs{AssetID: testAsset, Prefix: "runbook/"})
			So(err, ShouldBeNil)
			var list struct {
				Count int           `json:"count"`
				Notes []noteSummary `json:"notes"`
			}
			decode(t, listed, &list)
			So(list.Count, ShouldEqual, 2)
			So(list.Notes[0].Key, ShouldEqual, "runbook/failover")
			So(list.Notes[1].Key, ShouldEqual, "runbook/restore")
		})

		Convey("a deleted note is gone", func() {
			_, err := host.CallTool("note_put", putArgs{AssetID: testAsset, Key: "scratch", Content: "x"})
			So(err, ShouldBeNil)

			_, err = host.CallTool("note_delete", deleteArgs{AssetID: testAsset, Key: "scratch"})
			So(err, ShouldBeNil)

			_, err = host.CallTool("note_get", getArgs{AssetID: testAsset, Key: "scratch"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `has no note "scratch"`)
		})

		Convey("deleting a note that does not exist is an error, not a silent success", func() {
			_, err := host.CallTool("note_delete", deleteArgs{AssetID: testAsset, Key: "nope"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `has no note "nope"`)
		})

		Convey("a key the store cannot address is refused", func() {
			_, err := host.CallTool("note_put", putArgs{AssetID: testAsset, Key: "../escape", Content: "x"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "note key")
		})
	})

	Convey("notebooks are separated by the asset's configured name", t, func() {
		const otherAsset = int64(8)
		host := opskat.NewTestHost(
			opskat.WithAssetConfig(testAsset, notebookConfig{Notebook: "team-runbooks"}),
			opskat.WithAssetConfig(otherAsset, notebookConfig{Notebook: "other-team"}),
		)
		defer host.Close()

		_, err := host.CallTool("note_put", putArgs{AssetID: testAsset, Key: "shared", Content: "x"})
		So(err, ShouldBeNil)

		listed, err := host.CallTool("note_list", listArgs{AssetID: otherAsset})
		So(err, ShouldBeNil)
		var list struct {
			Notebook string `json:"notebook"`
			Count    int    `json:"count"`
		}
		decode(t, listed, &list)
		So(list.Notebook, ShouldEqual, "other-team")
		So(list.Count, ShouldEqual, 0)
	})

	Convey("the note limit from the asset config is enforced", t, func() {
		host := newHost(notebookConfig{Notebook: "small", MaxNotes: 1})
		defer host.Close()

		_, err := host.CallTool("note_put", putArgs{AssetID: testAsset, Key: "first", Content: "x"})
		So(err, ShouldBeNil)

		_, err = host.CallTool("note_put", putArgs{AssetID: testAsset, Key: "second", Content: "x"})
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "maxNotes=1")

		Convey("but overwriting an existing note still works at the limit", func() {
			_, err := host.CallTool("note_put", putArgs{AssetID: testAsset, Key: "first", Content: "y"})
			So(err, ShouldBeNil)
		})
	})

	Convey("an asset whose config is unusable fails the call instead of writing somewhere else", t, func() {
		host := newHost(notebookConfig{Notebook: ""})
		defer host.Close()

		_, err := host.CallTool("note_list", listArgs{AssetID: testAsset})
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "notebook")
	})
}

func TestValidateConfig(t *testing.T) {
	Convey("validateConfig", t, func() {
		Convey("accepts a usable notebook name", func() {
			So(validateConfig(notebookConfig{Notebook: "team-runbooks"}), ShouldBeEmpty)
			So(validateConfig(notebookConfig{Notebook: "team-runbooks", MaxNotes: 10}), ShouldBeEmpty)
		})

		Convey("names the offending field so the form can show it", func() {
			errs := validateConfig(notebookConfig{Notebook: "Team Runbooks"})
			So(errs, ShouldHaveLength, 1)
			So(errs[0].Field, ShouldEqual, "notebook")

			errs = validateConfig(notebookConfig{Notebook: "ok", MaxNotes: -1})
			So(errs, ShouldHaveLength, 1)
			So(errs[0].Field, ShouldEqual, "maxNotes")
		})
	})
}
