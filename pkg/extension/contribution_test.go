package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"

	. "github.com/smartystreets/goconvey/convey"
)

// fakePoint is a contribution point whose verdicts and attach outcome a test sets,
// recording what it was shown and what it attached.
type fakePoint struct {
	key       string
	checkErr  error
	attachErr error
	// onCheck sees the view the registry handed the point.
	onCheck  func(v ExtensionView)
	attached []string // extension names currently attached here
	log      *[]string
}

func (p *fakePoint) Key() string { return p.key }

func (p *fakePoint) Check(v ExtensionView) error {
	if p.onCheck != nil {
		p.onCheck(v)
	}
	return p.checkErr
}

func (p *fakePoint) Attach(v ExtensionView) (func(), error) {
	if p.attachErr != nil {
		return nil, p.attachErr
	}
	name := v.Name()
	p.attached = append(p.attached, name)
	*p.log = append(*p.log, "attach "+p.key)
	return func() {
		for i, n := range p.attached {
			if n == name {
				p.attached = append(p.attached[:i], p.attached[i+1:]...)
				break
			}
		}
		*p.log = append(*p.log, "revoke "+p.key)
	}, nil
}

// fakeView is an extension that contributes exactly the given raw entries.
func fakeView(name string, contributes map[string]string) ExtensionView {
	raw := make(map[string]json.RawMessage, len(contributes))
	for k, v := range contributes {
		raw[k] = json.RawMessage(v)
	}
	return newExtensionView(&Manifest{
		Manifest:    extmanifest.Manifest{Name: name, Permissions: extmanifest.Permissions{Kerberos: true}},
		contributes: raw,
	}, nil, nil, "", "")
}

func TestContributionRegistry(t *testing.T) {
	Convey("Given a registry of fake contribution points", t, func() {
		var log []string
		a := &fakePoint{key: "a", log: &log}
		b := &fakePoint{key: "b", log: &log}
		reg := NewContributionRegistry(a, b)

		Convey("an extension contributing only registered keys passes", func() {
			So(reg.Check(fakeView("ext", map[string]string{"a": `1`, "b": `2`})), ShouldBeNil)
		})

		Convey("a contributes key no point is registered for refuses the extension, naming it and the key", func() {
			err := reg.Check(fakeView("ext", map[string]string{"a": `1`, "widgets": `[]`}))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldStartWith, "ext: ")
			So(err.Error(), ShouldContainSubstring, "contributes.widgets")
		})

		Convey("a process missing the point for a present key refuses it the same way", func() {
			onlyA := NewContributionRegistry(&fakePoint{key: "a", log: &log})
			err := onlyA.Check(fakeView("ext", map[string]string{"a": `1`, "b": `2`}))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "contributes.b")
			So(onlyA.Attach(fakeView("ext", map[string]string{"b": `2`})), ShouldNotBeNil)
		})

		Convey("every point sees the whole extension: permissions and every contribution", func() {
			var seen ExtensionView
			a.onCheck = func(v ExtensionView) { seen = v }
			So(reg.Check(fakeView("ext", map[string]string{"b": `{"x":1}`})), ShouldBeNil)
			So(seen, ShouldNotBeNil)
			So(seen.Name(), ShouldEqual, "ext")
			So(seen.Manifest().Permissions.Kerberos, ShouldBeTrue)
			So(seen.Keys(), ShouldResemble, []string{"b"})
			So(string(seen.Contribution("b")), ShouldEqual, `{"x":1}`)
			So(seen.Contribution("a"), ShouldBeNil)
			So(seen.Plugin(), ShouldBeNil)
		})

		Convey("a point's refusal is the extension's, prefixed with its name", func() {
			b.checkErr = errors.New("b[item]: bad")
			err := reg.Check(fakeView("ext", nil))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldEqual, "ext: b[item]: bad")
		})

		Convey("attach runs the check first and attaches nothing on a refusal", func() {
			b.checkErr = errors.New("b[item]: bad")
			So(reg.Attach(fakeView("ext", nil)), ShouldNotBeNil)
			So(log, ShouldBeEmpty)
		})

		Convey("attach is all or nothing: a failed point revokes those already attached", func() {
			c := &fakePoint{key: "c", log: &log, attachErr: fmt.Errorf("c[x]: taken")}
			reg := NewContributionRegistry(a, b, c)
			err := reg.Attach(fakeView("ext", nil))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldEqual, "ext: c[x]: taken")
			So(log, ShouldResemble, []string{"attach a", "attach b", "revoke b", "revoke a"})
			So(a.attached, ShouldBeEmpty)
			So(b.attached, ShouldBeEmpty)

			Convey("and the name is free to attach again", func() {
				c.attachErr = nil
				So(reg.Attach(fakeView("ext", nil)), ShouldBeNil)
			})
		})

		Convey("revoke withdraws every point of that extension and only that one", func() {
			So(reg.Attach(fakeView("one", nil)), ShouldBeNil)
			So(reg.Attach(fakeView("two", nil)), ShouldBeNil)
			log = nil
			reg.Revoke("one")
			So(log, ShouldResemble, []string{"revoke b", "revoke a"})
			So(a.attached, ShouldResemble, []string{"two"})
			So(b.attached, ShouldResemble, []string{"two"})

			Convey("a second revoke, or one of a name never attached, does nothing", func() {
				log = nil
				reg.Revoke("one")
				reg.Revoke("never")
				So(log, ShouldBeEmpty)
			})
		})

		Convey("an extension is attached once until it is revoked", func() {
			So(reg.Attach(fakeView("ext", nil)), ShouldBeNil)
			err := reg.Attach(fakeView("ext", nil))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "already attached")
			reg.Revoke("ext")
			So(reg.Attach(fakeView("ext", nil)), ShouldBeNil)
		})
	})

	Convey("Two points claiming one key is a wiring error", t, func() {
		var log []string
		So(func() { NewContributionRegistry(&fakePoint{key: "a", log: &log}, &fakePoint{key: "a", log: &log}) }, ShouldPanic)
	})
}

// passPoint accepts its key and attaches nothing: the stand-in for the host's
// real contribution points (internal/extreg), which these runtime tests cannot
// import.
type passPoint string

func (p passPoint) Key() string                        { return string(p) }
func (passPoint) Check(ExtensionView) error            { return nil }
func (passPoint) Attach(ExtensionView) (func(), error) { return func() {}, nil }

// acceptingContributions is a registry that knows every describe() key and judges
// none of them.
func acceptingContributions() *ContributionRegistry {
	return NewContributionRegistry(
		passPoint(extwire.KeyAssetTypes), passPoint(extwire.KeyTools), passPoint(extwire.KeyPolicy),
		passPoint(extwire.KeySnippets), passPoint(extwire.KeyPages), passPoint(extwire.KeyDatabaseDrivers),
	)
}
