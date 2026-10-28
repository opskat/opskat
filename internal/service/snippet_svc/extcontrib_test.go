package snippet_svc

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/pkg/extension"
)

// snippetView is an extension contributing exactly the given raw describe() entries.
type snippetView map[string]string

func (snippetView) Name() string                   { return "ext" }
func (snippetView) Manifest() *extension.Manifest  { return &extension.Manifest{} }
func (snippetView) Plugin() *extension.Plugin      { return nil }
func (snippetView) Translate(_, key string) string { return key }
func (snippetView) SkillMD() string                { return "" }
func (snippetView) SkillDescription() string       { return "" }

func (v snippetView) Keys() []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	return keys
}

func (v snippetView) Contribution(key string) json.RawMessage {
	raw, ok := v[key]
	if !ok {
		return nil
	}
	return json.RawMessage(raw)
}

const oneAssetType = `[{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}]`

// checkSnippets judges a snippets block from an extension owning asset type "x".
func checkSnippets(snippets string) error {
	return ContributionPoint().Check(snippetView{"assetTypes": oneAssetType, "snippets": snippets})
}

func TestSnippetContributionPoint(t *testing.T) {
	Convey("Snippet declarations", t, func() {
		Convey("a valid block", func() {
			So(checkSnippets(`{
				"categories":[{"id":"kafka","assetType":"x","i18n":{"name":"category.kafka"}}],
				"seed":[
					{"key":"list-topics","name":"List topics","category":"kafka","content":"kafka-topics --list"},
					{"key":"ls","name":"ls","category":"shell","content":"ls -al"}
				]}`), ShouldBeNil)
		})

		Convey("an extension with no asset type may still seed a builtin category", func() {
			So(ContributionPoint().Check(snippetView{"snippets": `{"seed":[{"key":"ls","name":"ls","category":"shell","content":"ls -al"}]}`}), ShouldBeNil)
		})

		Convey("an extension contributing no snippets passes", func() {
			So(ContributionPoint().Check(snippetView{"assetTypes": oneAssetType}), ShouldBeNil)
		})

		Convey("a category id colliding with a builtin", func() {
			err := checkSnippets(`{"categories":[{"id":"shell","assetType":"x","i18n":{"name":"n"}}]}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "snippets.categories[shell]")
			So(err.Error(), ShouldContainSubstring, "builtin")
		})

		Convey("every category of the host registry is reserved and seedable", func() {
			reg := NewCategoryRegistry()
			for _, c := range reg.List() {
				So(reg.IsBuiltin(c.ID), ShouldBeTrue)
				err := checkSnippets(`{"categories":[{"id":"` + c.ID + `","assetType":"x","i18n":{"name":"n"}}]}`)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "snippets.categories["+c.ID+"]")
				So(checkSnippets(`{"seed":[{"key":"s","name":"s","category":"`+c.ID+`","content":"x"}]}`), ShouldBeNil)
			}
			So(reg.IsBuiltin("kafka"), ShouldBeFalse)
		})

		Convey("a duplicate category id", func() {
			err := checkSnippets(`{"categories":[` +
				`{"id":"kafka","assetType":"x","i18n":{"name":"n"}},` +
				`{"id":"kafka","assetType":"x","i18n":{"name":"n2"}}]}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})

		Convey("a category id in the wrong format", func() {
			err := checkSnippets(`{"categories":[{"id":"Kafka_1","assetType":"x","i18n":{"name":"n"}}]}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "must match")
		})

		Convey("a category attached to an asset type this extension does not own", func() {
			err := checkSnippets(`{"categories":[{"id":"missing","assetType":"other","i18n":{"name":"n"}}]}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "asset types")
		})

		Convey("a seed referencing an unknown category", func() {
			err := checkSnippets(`{"seed":[{"key":"x","name":"x","category":"nope","content":"echo"}]}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "snippets.seed[x]")
			So(err.Error(), ShouldContainSubstring, "neither builtin nor declared")
		})

		Convey("duplicate seed keys", func() {
			err := checkSnippets(`{"seed":[` +
				`{"key":"k1","name":"a","category":"shell","content":"x"},` +
				`{"key":"k1","name":"b","category":"shell","content":"y"}]}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})

		Convey("a seed key in the wrong format", func() {
			err := checkSnippets(`{"seed":[{"key":"BadKey!","name":"a","category":"shell","content":"x"}]}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "must match")
		})

		Convey("a seed with empty content", func() {
			err := checkSnippets(`{"seed":[{"key":"k1","name":"a","category":"shell","content":"   "}]}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "content")
		})
	})
}
