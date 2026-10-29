package extension

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// reviewerDescriptor 是最小可加载描述符再加一段 reviewer。
func reviewerDescriptor(reviewer string) []byte {
	return desc(`"reviewer":` + reviewer)
}

func TestReviewerDescriptor(t *testing.T) {
	valid := `{"questions":[{"id":"destructive","i18n":{"name":{"zh-CN":"破坏数据","en":"Destroys data"}}}],"maxItems":32,"maxCommandLen":4000}`

	Convey("describe() 的 reviewer 段必须自洽", t, func() {
		Convey("合法声明被接受并原样保留", func() {
			d, err := ParseDescriptor(reviewerDescriptor(valid))
			So(err, ShouldBeNil)
			So(d.Reviewer, ShouldNotBeNil)
			So(d.Reviewer.MaxItems, ShouldEqual, 32)
			So(d.Reviewer.MaxCommandLen, ShouldEqual, 4000)
			So(d.Reviewer.Questions[0].ID, ShouldEqual, "destructive")
			So(d.Reviewer.Questions[0].I18n.Name["en"], ShouldEqual, "Destroys data")
		})

		Convey("问题 id 重复、为空或含冒号", func() {
			_, err := ParseDescriptor(reviewerDescriptor(`{"questions":[{"id":"a","i18n":{"name":{"en":"A"}}},{"id":"a","i18n":{"name":{"en":"A"}}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")

			_, err = ParseDescriptor(reviewerDescriptor(`{"questions":[{"id":"","i18n":{"name":{"en":"A"}}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "id")

			_, err = ParseDescriptor(reviewerDescriptor(`{"questions":[{"id":"bad:id","i18n":{"name":{"en":"A"}}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "id")
		})

		Convey("没有问题、没有名称、条数或长度不是正数", func() {
			_, err := ParseDescriptor(reviewerDescriptor(`{"questions":[]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "questions")

			_, err = ParseDescriptor(reviewerDescriptor(`{"questions":[{"id":"a","i18n":{"name":{}}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "name")

			_, err = ParseDescriptor(reviewerDescriptor(`{"questions":[{"id":"a","i18n":{"name":{"en":"A"}}}],"maxItems":0}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "maxItems")

			_, err = ParseDescriptor(reviewerDescriptor(`{"questions":[{"id":"a","i18n":{"name":{"en":"A"}}}],"maxItems":1,"maxCommandLen":-1}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "maxCommandLen")
		})
	})
}

func TestCommandReviewCapabilityPair(t *testing.T) {
	reviewer := &ReviewerDef{Questions: []ReviewQuestion{{ID: "destructive", I18n: I18nNames{Name: map[string]string{"en": "Destroys data"}}}}, MaxItems: 8, MaxCommandLen: 100}
	base := func() *Manifest {
		m, err := ParseManifest([]byte(`{"name":"rev","version":"1.0.0","hostABI":"2.6","backend":{"runtime":"wasm","binary":"main.wasm"},"capabilities":{"commandReview":true}}`))
		So(err, ShouldBeNil)
		m.apply(&Descriptor{
			AssetTypes: []AssetTypeDef{{Type: "x", ConfigSchema: map[string]any{"type": "object", "properties": map[string]any{"e": map[string]any{"type": "string"}}}}},
			Policies:   PoliciesDef{Type: "x"},
		})
		return m
	}

	Convey("能力与 reviewer 段必须成对", t, func() {
		Convey("声明了能力却没有 reviewer", func() {
			err := base().validateReviewer()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "commandReview")
			So(err.Error(), ShouldContainSubstring, "reviewer")
		})

		// 审核是 2.6 加的：2.3–2.5 是数据库驱动的几步，那时的宿主不认识审核。
		Convey("声明审核能力要 hostABI 2.6", func() {
			m := base()
			m.HostABI = "2.5"
			m.Reviewer = reviewer
			err := m.validateReviewer()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "2.6")
		})

		Convey("有 reviewer 却没有能力", func() {
			m := base()
			m.Capabilities.CommandReview = false
			m.Reviewer = reviewer
			err := m.validateReviewer()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "commandReview")
		})

		Convey("两者都有则通过", func() {
			m := base()
			m.Reviewer = reviewer
			So(m.validateReviewer(), ShouldBeNil)
			So(m.IsCommandReviewer(), ShouldBeTrue)
		})

		Convey("两者都没有则不是审核器", func() {
			m := base()
			m.Capabilities.CommandReview = false
			So(m.validateReviewer(), ShouldBeNil)
			So(m.IsCommandReviewer(), ShouldBeFalse)
		})
	})
}

func TestReviewCommandsValidation(t *testing.T) {
	questions := []ReviewQuestion{{ID: "destructive"}, {ID: "remote_code"}}

	decode := func(raw string) (json.RawMessage, error) {
		return json.RawMessage(raw), nil
	}

	Convey("审核结果整批校验", t, func() {
		Convey("条数与输入一致且问题 id 都已声明", func() {
			out, err := validateReviewResults(2, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"pass"},{"outcome":"reject","failed":["remote_code"],"scores":{"remote_code":0.4}}]`)
			})
			So(err, ShouldBeNil)
			So(len(out), ShouldEqual, 2)
			So(out[0].Outcome, ShouldEqual, ReviewOutcomePass)
			So(out[1].Failed, ShouldResemble, []string{"remote_code"})
			So(out[1].Scores, ShouldResemble, map[string]float64{"remote_code": 0.4})
		})

		Convey("条数不一致", func() {
			_, err := validateReviewResults(2, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"pass"}]`)
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ReasonInvalidResult)
		})

		Convey("未声明的问题 id", func() {
			_, err := validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"reject","failed":["nope"]}]`)
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "nope")
		})

		Convey("reject 不带问题 id", func() {
			_, err := validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"reject"}]`)
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "failed")
		})

		Convey("未知 outcome 或 JSON 解析失败", func() {
			_, err := validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"maybe"}]`)
			})
			So(err, ShouldNotBeNil)

			_, err = validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`not-json`)
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ReasonInvalidResult)
		})

		Convey("error 必须带原因，pass 不能带问题", func() {
			_, err := validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"error"}]`)
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "reason")

			_, err = validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"pass","failed":["destructive"]}]`)
			})
			So(err, ShouldNotBeNil)
		})

		Convey("评分只能属于未通过的题目", func() {
			_, err := validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"reject","failed":["destructive"],"scores":{"remote_code":0.4}}]`)
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not in failed")

			_, err = validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"reject","failed":["destructive"],"scores":{"destructive":1.5}}]`)
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "outside [0,1]")

			_, err = validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return decode(`[{"outcome":"pass","scores":{"destructive":0.1}}]`)
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "scores belong on reject")
		})

		Convey("调用本身失败同样是无效结果", func() {
			_, err := validateReviewResults(1, questions, func() (json.RawMessage, error) {
				return nil, errInvalid
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, ReasonInvalidResult)
		})
	})
}

// errInvalid 是测试里的调用失败。
var errInvalid = errString("guest failed")

type errString string

func (e errString) Error() string { return string(e) }

// 纯审核扩展：入口是 review_commands，不是对资产 exec，所以可以不声明资产类型和策略面；
// 但它也就没有地方调用工具，声明工具要拒绝。
func TestParseDescriptorReviewerOnly(t *testing.T) {
	reviewer := `"reviewer":{"questions":[{"id":"a","i18n":{"name":{"en":"A"}}}],"maxItems":4,"maxCommandLen":100}`
	Convey("只声明 reviewer 的描述符", t, func() {
		Convey("没有资产类型和策略面也能加载", func() {
			d, err := ParseDescriptor([]byte(`{` + reviewer + `}`))
			So(err, ShouldBeNil)
			So(d.Reviewer, ShouldNotBeNil)
			So(d.AssetTypes, ShouldBeEmpty)
		})
		Convey("声明了工具就拒绝：没有资产，工具调不到", func() {
			_, err := ParseDescriptor([]byte(`{` + reviewer + `,"tools":[{"name":"t","i18n":{"description":"d"},"parameters":{"type":"object"}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "assetTypes")
		})
		Convey("既没有资产类型也没有 reviewer 仍然拒绝", func() {
			_, err := ParseDescriptor([]byte(`{"policies":{"type":"x"}}`))
			So(err, ShouldNotBeNil)
		})
	})
}
