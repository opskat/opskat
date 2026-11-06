package extension

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/pkg/extmanifest"

	. "github.com/smartystreets/goconvey/convey"
)

func TestHostOpAssetGetConfig(t *testing.T) {
	Convey("asset.get_config only ever reads the invocation's own asset", t, func() {
		host := newRecordedHost()
		host.configs[7] = json.RawMessage(`{"own":true}`)
		host.configs[99] = json.RawMessage(`{"secret":"someone else's"}`)

		call := func(inv *invocation, params string) ([]byte, error) {
			req, err := json.Marshal(map[string]any{"op": "asset.get_config", "params": json.RawMessage(params)})
			So(err, ShouldBeNil)
			return hostCall(withInvocation(context.Background(), inv), host, req)
		}

		Convey("the config returned is the asset the host scoped the call to", func() {
			inv := newInvocation("t#1", nil).scopedTo(&AssetRef{ID: 7, Type: "fixture"})
			out, err := call(inv, `{}`)
			So(err, ShouldBeNil)
			So(string(out), ShouldEqual, `{"own":true}`)
		})

		Convey("a guest naming another asset id is refused, not served", func() {
			inv := newInvocation("t#2", nil).scopedTo(&AssetRef{ID: 7, Type: "fixture"})
			out, err := call(inv, `{"asset_id":99}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "asset_id")
			So(out, ShouldBeNil)
		})

		Convey("a call with no asset gets a clear error", func() {
			_, err := call(newInvocation("t#3", nil), `{"asset_id":99}`)
			So(err, ShouldNotBeNil)
			_, err = call(newInvocation("t#4", nil), `{}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not scoped to an asset")
		})
	})
}

// staticTickets answers "ticket:<spn>" for every SPN.
type staticTickets struct{ asked []string }

func (s *staticTickets) Ticket(_ context.Context, spn string) ([]byte, error) {
	s.asked = append(s.asked, spn)
	return []byte("ticket:" + spn), nil
}

// A Kerberos ticket needs both halves: a scope that carries a ticket source (a
// database asset that logs in with Kerberos) and the manifest's kerberos
// permission. Missing either is refused saying which.
func TestHostOpKerberosTicket(t *testing.T) {
	Convey("Given the capability layer over a recorded host", t, func() {
		manifest := &Manifest{Manifest: extmanifest.Manifest{Name: "krb", Version: "1.0.0"}}
		host := NewCapabilityHost(newRecordedHost(), manifest, "/tmp/ext")
		ask := func(inv *invocation) ([]byte, error) {
			return hostCall(withInvocation(context.Background(), inv), host,
				[]byte(`{"op":"auth.kerberos_ticket","params":{"spn":"oracle/db.internal"}}`))
		}

		Convey("a call whose scope has no ticket source is refused saying so, whatever the manifest", func() {
			for _, declared := range []bool{false, true} {
				manifest.Permissions.Kerberos = declared
				for _, inv := range []*invocation{
					newInvocation("t#1", nil),
					newInvocation("t#2", nil).scopedTo(&AssetRef{ID: 7, Type: "fixture"}),
					newInvocation("t#3", nil).bindScope(&Scope{Asset: &AssetRef{ID: 8}}),
				} {
					_, err := ask(inv)
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldContainSubstring, "no Kerberos ticket source")
				}
			}
		})

		Convey("a ticket scope without the kerberos permission is refused naming the permission", func() {
			tickets := &staticTickets{}
			_, err := ask(newInvocation("t#4", nil).bindScope(&Scope{Asset: &AssetRef{ID: 8}, Tickets: tickets}))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "permissions.kerberos")
			So(tickets.asked, ShouldBeEmpty)
		})

		Convey("a ticket scope with the kerberos permission gets the source's ticket", func() {
			manifest.Permissions.Kerberos = true
			tickets := &staticTickets{}
			out, err := ask(newInvocation("t#5", nil).bindScope(&Scope{Asset: &AssetRef{ID: 8}, Tickets: tickets}))
			So(err, ShouldBeNil)
			So(string(out), ShouldEqual, "ticket:oracle/db.internal")
			So(tickets.asked, ShouldResemble, []string{"oracle/db.internal"})
		})
	})
}

// A scope bound with Ops makes every other host call fail before it reaches the
// host; io.open is narrowed per stream type.
func TestHostCallScopeOps(t *testing.T) {
	Convey("Given a call whose scope allows only logging and TCP streams", t, func() {
		host := newRecordedHost()
		inv := newInvocation("t#1", nil).bindScope(&Scope{Asset: &AssetRef{ID: 8}, Ops: []string{OpLog, IOOpenOp("tcp")}})
		call := func(req string) error {
			_, err := hostCall(withInvocation(context.Background(), inv), host, []byte(req))
			return err
		}

		Convey("an allowed op runs", func() {
			So(call(`{"op":"log","params":{"level":"info","msg":"hi"}}`), ShouldBeNil)
			So(host.logs, ShouldResemble, []string{"info:hi"})
		})

		Convey("any other op is refused by name and never reaches the host", func() {
			err := call(`{"op":"kv.get","params":{"key":"k"}}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `"kv.get" is not allowed in this call's scope`)

			err = call(`{"op":"io.open","params":{"type":"file","path":"/etc/passwd","mode":"read"}}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `"io.open:file" is not allowed in this call's scope`)
			So(host.closedCount(), ShouldEqual, 0)
			So(host.resources, ShouldBeEmpty)
		})

		Convey("a call with no Ops is not restricted", func() {
			free := newInvocation("t#2", nil).scopedTo(&AssetRef{ID: 8})
			_, err := hostCall(withInvocation(context.Background(), free), host, []byte(`{"op":"kv.get","params":{"key":"k"}}`))
			So(err, ShouldBeNil)
		})
	})
}
