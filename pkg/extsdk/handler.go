package opskat

import (
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/pkg/extwire"
)

// dispatch routes a function call to the registered handler.
func dispatch(fnName string, input []byte) (json.RawMessage, error) {
	switch fnName {
	case "describe":
		return dispatchDescribe()
	case "execute_tool":
		return dispatchTool(input)
	case "check_policy":
		return dispatchPolicy(input)
	case "validate_config":
		return dispatchConfigValidator(input)
	case "test_connection":
		return dispatchTestConnection(input)
	case extwire.DriverFunction:
		return dispatchDatabaseDriver(input)
	default:
		return nil, fmt.Errorf("unknown function: %s", fnName)
	}
}

// dispatchDescribe answers describe(): what the extension can do, generated from
// the same registries dispatch serves calls from, so the host's view of the
// extension and the code that runs cannot disagree — a tool the guest can
// execute is a tool describe reports, with the schema its handler actually
// decodes. The answer is encoded with the wire types the host decodes it with
// (pkg/extwire); a contribution point the extension registered nothing for is
// left out.
func dispatchDescribe() (json.RawMessage, error) {
	d := extwire.Describe{
		Icon: meta.Icon,
		I18n: extwire.I18n{DisplayName: meta.DisplayName, Description: meta.Description},
	}

	assetTypeWire := make([]extwire.AssetType, 0, len(assetTypes))
	for _, at := range assetTypes {
		assetTypeWire = append(assetTypeWire, extwire.AssetType{
			Type:           at.typ,
			I18n:           extwire.I18nName{Name: at.name},
			ConfigSchema:   at.schema,
			Connection:     at.connection,
			Auth:           at.auth,
			TestConnection: at.testConnection != nil,
		})
	}

	toolWire := make([]extwire.Tool, 0, len(toolOrder))
	for _, name := range toolOrder {
		t := tools[name]
		if err := t.check(); err != nil {
			return nil, err
		}
		toolWire = append(toolWire, extwire.Tool{
			Name:          t.name,
			I18n:          extwire.I18nDesc{Description: t.doc},
			Parameters:    t.schema,
			PolicyAction:  t.action,
			PolicyActions: t.actions,
			PageOnly:      t.pageOnly,
			TimeoutMs:     t.timeout.Milliseconds(),
			NoTimeout:     t.noTimeout,
			Large:         t.large,
		})
	}

	policy := extwire.Policy{Type: meta.PolicyType}
	for _, g := range policyGroups {
		rules := map[string]any{}
		if len(g.allow) > 0 {
			rules["allow_list"] = g.allow
		}
		if len(g.deny) > 0 {
			rules["deny_list"] = g.deny
		}
		policy.Groups = append(policy.Groups, extwire.PolicyGroup{
			ID:     g.id,
			I18n:   extwire.I18nNameDesc{Name: g.name, Description: g.description},
			Policy: rules,
		})
		if g.isDefault {
			policy.Default = append(policy.Default, g.id)
		}
	}

	var snippets extwire.Snippets
	for _, c := range snippetCats {
		snippets.Categories = append(snippets.Categories, extwire.SnippetCategory{
			ID: c.id, AssetType: c.assetType, I18n: extwire.I18nName{Name: c.name},
		})
	}
	for _, s := range snippetSeeds {
		snippets.Seed = append(snippets.Seed, extwire.SeedSnippet(s))
	}

	pages := extwire.Pages{Entry: frontendSpec.Entry, Styles: frontendSpec.Styles}
	for _, p := range frontendSpec.Pages {
		pages.Pages = append(pages.Pages, extwire.Page{
			ID:        p.ID,
			Slot:      p.Slot,
			I18n:      extwire.I18nName{Name: p.Name},
			Component: p.Component,
		})
	}

	contributions := []struct {
		key     string
		present bool
		value   any
	}{
		{extwire.KeyAssetTypes, len(assetTypeWire) > 0, assetTypeWire},
		{extwire.KeyTools, len(toolWire) > 0, toolWire},
		{extwire.KeyPolicy, policy.Type != "" || len(policy.Groups) > 0, policy},
		{extwire.KeySnippets, len(snippets.Categories) > 0 || len(snippets.Seed) > 0, snippets},
		{extwire.KeyPages, frontendSpec.Entry != "", pages},
		{extwire.KeyDatabaseDrivers, len(databaseDrivers) > 0, databaseDrivers},
	}
	for _, c := range contributions {
		if !c.present {
			continue
		}
		if err := d.Put(c.key, c.value); err != nil {
			return nil, err
		}
	}
	return json.Marshal(d)
}

// toolCall is the shape of both execute_tool and check_policy input: the host
// asks the same question about the same call, once to classify it and once to run it.
//
// asset is only on the execute_tool side of that pair — check_policy is answered
// from the tool's own registration, and the asset half of the decision (which
// permission groups are granted on it) is the host's.
type toolCall struct {
	Tool  string          `json:"tool"`
	Args  json.RawMessage `json:"args"`
	Asset Asset           `json:"asset"`
}

func parseToolCall(input []byte) (*toolEntry, toolCall, error) {
	var req toolCall
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, req, fmt.Errorf("parse tool request: %w", err)
	}
	entry, ok := tools[req.Tool]
	if !ok {
		return nil, req, fmt.Errorf("unknown tool: %s", req.Tool)
	}
	return entry, req, nil
}

func dispatchTool(input []byte) (json.RawMessage, error) {
	entry, req, err := parseToolCall(input)
	if err != nil {
		return nil, err
	}
	rejected, err := entry.checkArgs(req.Args)
	if err != nil {
		return nil, err
	}
	if rejected != nil {
		return nil, rejected
	}
	result, err := entry.invoke(&ToolContext{Tool: req.Tool, Args: req.Args, Asset: req.Asset, Events: newEventWriter()})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

// dispatchPolicy answers from the tool's own registration. The action a tool
// requests is part of declaring the tool, so there is no per-tool switch here to
// fall out of step with the handler table.
//
// The reply has two shapes. {"action","resource"} is the 2.0/2.1 wire: one literal
// resource, what Policy/Resource and PolicyFunc answer. {"action","resources"} is
// the 2.2 wire PolicyResources answers — a list (never null) whose '*' / '?' are
// wildcards. Which shape a tool answers is fixed by how it registered, so a
// single-resource extension's reply is byte-for-byte what it always was.
//
// A tool declaring RejectArgs may answer a third shape first, {"reject":"<reason>"}
// (2.2 too): no classification, the tool refusing the arguments themselves.
func dispatchPolicy(input []byte) (json.RawMessage, error) {
	entry, req, err := parseToolCall(input)
	if err != nil {
		return nil, err
	}
	rejected, err := entry.checkArgs(req.Args)
	if err != nil {
		return nil, err
	}
	if rejected != nil {
		return json.Marshal(struct {
			Reject string `json:"reject"`
		}{rejected.Reason})
	}
	if entry.classify == nil {
		resource := ""
		if entry.resource != nil {
			resource = entry.resource(req.Args)
		}
		return json.Marshal(map[string]string{"action": entry.action, "resource": resource})
	}
	action, resources, err := entry.classify(req.Args)
	if err != nil {
		return nil, err
	}
	if !entry.multiResource {
		return json.Marshal(map[string]string{"action": action, "resource": resources[0]})
	}
	if resources == nil {
		resources = []string{}
	}
	return json.Marshal(struct {
		Action    string   `json:"action"`
		Resources []string `json:"resources"`
	}{action, resources})
}

// testConnectionCall is the shape of test_connection's input: which asset
// type to test (an extension may register several) and its guest-visible
// config — the form's submitted values, not a saved asset read back from the
// host, since there may be no saved asset yet (see AssetTypeReg.TestConnection).
type testConnectionCall struct {
	AssetType string          `json:"assetType"`
	Config    json.RawMessage `json:"config"`
}

func dispatchTestConnection(input []byte) (json.RawMessage, error) {
	var req testConnectionCall
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("parse test connection request: %w", err)
	}
	for _, at := range assetTypes {
		if at.typ != req.AssetType {
			continue
		}
		if at.testConnection == nil {
			return nil, fmt.Errorf("asset type %q does not declare a test connection handler", req.AssetType)
		}
		if err := at.testConnection(req.Config); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{})
	}
	return nil, fmt.Errorf("unknown asset type: %s", req.AssetType)
}

func dispatchConfigValidator(input []byte) (json.RawMessage, error) {
	if configValidator == nil {
		return json.Marshal([]ValidationError{})
	}
	errors := configValidator(input)
	if errors == nil {
		errors = []ValidationError{}
	}
	return json.Marshal(errors)
}
