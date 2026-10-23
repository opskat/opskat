package opskat

import "encoding/json"

// Log sends a log message to the host.
func Log(level, msg string) {
	hostLog(level, msg)
}

// DialogOptions for FileDialog.
type DialogOptions struct {
	Title       string   `json:"title"`
	DefaultName string   `json:"defaultName"`
	Filters     []string `json:"filters"`
}

// FileDialog opens a native file dialog (open/save).
func FileDialog(dialogType string, opts DialogOptions) (string, error) {
	params, _ := json.Marshal(map[string]any{
		"type": dialogType,
		"opts": opts,
	})
	return hostFileDialog(params)
}

// KVGet retrieves a value from the extension's private key-value store.
func KVGet(key string) ([]byte, error) {
	return hostKVGet(key)
}

// KVSet stores a value in the extension's private key-value store.
func KVSet(key string, value []byte) error {
	return hostKVSet(key, value)
}

// KerberosTicket asks the host for the Kerberos ticket (the AP-REQ bytes, to be
// written into the login handshake as they are) for service principal spn —
// dbdriver.KerberosSPN(service, host) of the names the database sent. It only
// answers inside a session of a database driver whose asset logs in with
// Kerberos (the extension declares capabilities.databaseKerberos, the driver the
// dbdriver.FieldKerberos field group); otherwise, or when the asset pins another
// service principal, the host refuses with the reason. A driver usually reaches
// it as OpenRequest.Kerberos.Ticket.
func KerberosTicket(spn string) ([]byte, error) {
	return hostKerberosTicket(spn)
}
