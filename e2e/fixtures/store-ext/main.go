// Command store-ext is the package the e2e extension-store mock publishes: the
// smallest extension the host accepts — presentation metadata, one asset type
// with a one-field form and its policy face — under names no harness extension
// uses, so a store install lands without colliding with anything. Built for
// wasip1 by fixtures/ext-store-mock at startup.
package main

import opskat "github.com/opskat/opskat/pkg/extsdk"

func main() {}

type demoConfig struct {
	Label string `json:"label" title:"config.label.title"`
}

func init() {
	opskat.Extension(opskat.Meta{
		Icon:        "package",
		DisplayName: "extension.displayName",
		Description: "extension.description",
		PolicyType:  "storedemo",
	})
	opskat.AssetType[demoConfig]("storedemo").Name("assetType.storedemo.name")
}
