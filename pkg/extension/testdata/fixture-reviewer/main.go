// Command fixture-reviewer is the WASM extension tests and verification runs use
// as the command reviewer (opskat.CommandReviewer): a host-only review tool and the
// commandReviewer contribution naming it, with three questions — destructive,
// disruptive, remote_code. Built on demand by extensiontest.InstallReviewerFixture
// and installed into the sandbox (e2e/harness/env.js), never shipped.
//
// With its "endpoint" setting empty it judges by keyword (rules); with it set, it
// asks the service there instead (askService): POST <endpoint>/review with
// {"assetType","command"}, whose JSON reply {outcome, failed, scores, reason} is
// the verdict — the "apiKey" setting reaches that service as a bearer token the
// host injects, never through the guest.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

func main() {}

type settings struct {
	Endpoint string `json:"endpoint,omitempty" title:"Review service URL" desc:"Leave empty to judge by the fixture's keyword rules" format:"endpoint"`
	APIKey   string `json:"apiKey,omitempty" title:"API key" format:"password"`
}

var questions = []opskat.ReviewQuestion{
	{ID: "destructive", Names: map[string]string{"en": "may destroy or irreversibly change data", "zh-CN": "可能破坏或不可恢复地修改数据"}},
	{ID: "disruptive", Names: map[string]string{"en": "may interrupt services", "zh-CN": "可能中断服务"}},
	{ID: "remote_code", Names: map[string]string{"en": "downloads and runs code", "zh-CN": "会下载并执行代码"}},
}

// maxCommandLen is the longest command the fixture takes.
const maxCommandLen = 2000

func init() {
	opskat.Extension(opskat.Meta{
		DisplayName: "Reviewer Fixture",
		Description: "Command reviewer fixture used by host tests and verification runs",
	})
	opskat.Settings[settings]().Auth(opskat.Auth{Groups: []opskat.AuthGroup{{
		Bindings: []opskat.AuthBinding{{In: "header", Name: "Authorization", Value: "Bearer {{apiKey}}"}},
	}}})
	opskat.CommandReviewer(questions, maxCommandLen, review)
}

func review(ctx *opskat.ToolContext, item opskat.ReviewItem) opskat.ReviewResult {
	s, err := ctx.Settings()
	if err != nil {
		opskat.Log("error", "read settings: "+err.Error())
		return opskat.ReviewResult{Outcome: opskat.ReviewError, Reason: "not_configured"}
	}
	if endpoint, _ := s.Values["endpoint"].(string); endpoint != "" {
		return askService(strings.TrimRight(endpoint, "/"), item)
	}
	return rules(item.Command)
}

// rules is the keyword verdict:
//
//	"fixture-error"     → error "unavailable"
//	"fixture-slow"      → runs past any review timeout (spins 60s)
//	"rm -rf"            → reject destructive (score 0.9)
//	"curl" and "| sh"   → reject remote_code (score 0.8)
//	anything else       → pass
func rules(command string) opskat.ReviewResult {
	switch {
	case strings.Contains(command, "fixture-error"):
		return opskat.ReviewResult{Outcome: opskat.ReviewError, Reason: "unavailable"}
	case strings.Contains(command, "fixture-slow"):
		// Spin in bytecode rather than sleep in a host call, so the host's
		// deadline stops the call.
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); {
		}
		return opskat.ReviewResult{Outcome: opskat.ReviewPass}
	case strings.Contains(command, "rm -rf"):
		return opskat.ReviewResult{Outcome: opskat.ReviewReject, Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": 0.9}}
	case strings.Contains(command, "curl") && strings.Contains(command, "| sh"):
		return opskat.ReviewResult{Outcome: opskat.ReviewReject, Failed: []string{"remote_code"}, Scores: map[string]float64{"remote_code": 0.8}}
	}
	return opskat.ReviewResult{Outcome: opskat.ReviewPass}
}

// askService asks the review service at endpoint about the command; a request
// or reply that fails is an "unavailable" error.
func askService(endpoint string, item opskat.ReviewItem) opskat.ReviewResult {
	body, err := json.Marshal(map[string]string{"assetType": item.AssetType, "command": item.Command})
	if err != nil {
		return unavailable(err)
	}
	client := &http.Client{Transport: opskat.NewHTTPTransport()}
	resp, err := client.Post(endpoint+"/review", "application/json", bytes.NewReader(body))
	if err != nil {
		return unavailable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return unavailable(fmt.Errorf("review service answered %s", resp.Status))
	}
	var verdict opskat.ReviewResult
	if err := json.NewDecoder(resp.Body).Decode(&verdict); err != nil {
		return unavailable(err)
	}
	return verdict
}

func unavailable(err error) opskat.ReviewResult {
	opskat.Log("warn", "review service: "+err.Error())
	return opskat.ReviewResult{Outcome: opskat.ReviewError, Reason: "unavailable"}
}
