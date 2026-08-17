package command

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestResolvePolicyLang(t *testing.T) {
	Convey("resolvePolicyLang", t, func() {
		Convey("LC_ALL takes priority over LC_MESSAGES and LANG", func() {
			So(resolvePolicyLang("zh_CN.UTF-8", "en_US.UTF-8", "en_US.UTF-8"), ShouldEqual, "zh-cn")
			So(resolvePolicyLang("en_US.UTF-8", "zh_CN.UTF-8", "zh_CN.UTF-8"), ShouldEqual, "en")
		})

		Convey("LC_MESSAGES is consulted when LC_ALL is empty", func() {
			So(resolvePolicyLang("", "zh_CN.UTF-8", "en_US.UTF-8"), ShouldEqual, "zh-cn")
			So(resolvePolicyLang("", "en_US.UTF-8", "zh_CN.UTF-8"), ShouldEqual, "en")
		})

		Convey("LANG is consulted when LC_ALL and LC_MESSAGES are empty", func() {
			So(resolvePolicyLang("", "", "zh_CN.UTF-8"), ShouldEqual, "zh-cn")
			So(resolvePolicyLang("", "", "en_US.UTF-8"), ShouldEqual, "en")
		})

		Convey("language prefix is taken from the value", func() {
			So(resolvePolicyLang("", "", "zh"), ShouldEqual, "zh-cn")
			So(resolvePolicyLang("", "", "zh_TW.Big5"), ShouldEqual, "zh-cn")
			So(resolvePolicyLang("", "", "ZH_cn"), ShouldEqual, "zh-cn")
			So(resolvePolicyLang("", "", "en"), ShouldEqual, "en")
		})

		Convey("C and POSIX fall back to en without consulting lower-priority variables", func() {
			So(resolvePolicyLang("C", "", "zh_CN.UTF-8"), ShouldEqual, "en")
			So(resolvePolicyLang("", "POSIX", "zh_CN.UTF-8"), ShouldEqual, "en")
		})

		Convey("unrecognized values and empty environment fall back to en", func() {
			So(resolvePolicyLang("", "", "fr_FR.UTF-8"), ShouldEqual, "en")
			So(resolvePolicyLang("", "", ""), ShouldEqual, "en")
			So(resolvePolicyLang("", "", "/invalid"), ShouldEqual, "en")
		})
	})
}
