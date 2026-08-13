package assettype

import (
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
)

func passwordAutomationContract(configFields, approvalFields []string, usernameField string, normalize func(map[string]any) error) AutomationContract {
	return newAutomationContract(configFields, approvalFields, normalize,
		func(args map[string]any) (CredentialPlan, error) {
			return passwordCredentialPlan(args, "password", usernameField)
		},
		func(args map[string]any, binding CredentialBinding) (map[string]any, error) {
			return bindPasswordCredential(args, binding, "password")
		},
	)
}

func normalizeDefaultPort(port int) func(map[string]any) error {
	return func(args map[string]any) error {
		if ArgInt(args, "port") == 0 {
			args["port"] = port
		}
		return nil
	}
}

func (*sshHandler) AutomationContract() AutomationContract {
	return newAutomationContract(
		[]string{"host", "port", "username", "auth_type", "password", "private_key", "passphrase", "credential_id", "agent_source_id", "agent_key_fingerprint", "ssh_asset_id"},
		[]string{"host", "port", "username", "auth_type", "agent_source_id", "agent_key_fingerprint", "ssh_asset_id"},
		normalizeSSHAutomation,
		sshCredentialPlan,
		bindSSHCredential,
	)
}

func normalizeSSHAutomation(args map[string]any) error {
	if ArgInt(args, "port") == 0 {
		args["port"] = 22
	}
	if ArgString(args, "auth_type") == "" {
		switch {
		case ArgString(args, "private_key") != "":
			args["auth_type"] = asset_entity.AuthTypeKey
		case ArgString(args, "password") != "":
			args["auth_type"] = asset_entity.AuthTypePassword
		}
	}
	return nil
}

func sshCredentialPlan(args map[string]any) (CredentialPlan, error) {
	authType := ArgString(args, "auth_type")
	credentialID, _, err := positiveInt64Arg(args, "credential_id")
	if err != nil {
		return CredentialPlan{}, err
	}
	password := ArgString(args, "password")
	privateKey := ArgString(args, "private_key")
	passphrase := ArgString(args, "passphrase")
	if passphrase != "" && privateKey == "" {
		return CredentialPlan{}, fmt.Errorf("passphrase requires private_key")
	}
	if authType == asset_entity.AuthTypeAgent {
		return noCredentialPlan("password", "private_key", "passphrase", "credential_id")(args)
	}
	sources := 0
	if credentialID > 0 {
		sources++
	}
	if password != "" {
		sources++
	}
	if privateKey != "" {
		sources++
	}
	if sources > 1 {
		return CredentialPlan{}, fmt.Errorf("credential_id, password, and private_key are mutually exclusive")
	}
	if credentialID > 0 {
		accepted := []string{credential_entity.TypePassword, credential_entity.TypeSSHKey}
		switch authType {
		case asset_entity.AuthTypePassword:
			accepted = []string{credential_entity.TypePassword}
		case asset_entity.AuthTypeKey:
			accepted = []string{credential_entity.TypeSSHKey}
		}
		return CredentialPlan{Kind: CredentialKindReference, ReferenceID: credentialID, AcceptedTypes: accepted}, nil
	}
	if password != "" {
		if authType != "" && authType != asset_entity.AuthTypePassword {
			return CredentialPlan{}, fmt.Errorf("password conflicts with auth_type %q", authType)
		}
		return CredentialPlan{Kind: CredentialKindPassword, Plaintext: password, Username: ArgString(args, "username"), UsernameField: "username"}, nil
	}
	if privateKey != "" {
		if authType != "" && authType != asset_entity.AuthTypeKey {
			return CredentialPlan{}, fmt.Errorf("private_key conflicts with auth_type %q", authType)
		}
		return CredentialPlan{Kind: CredentialKindSSHKey, PrivateKey: privateKey, Passphrase: passphrase, Username: ArgString(args, "username"), UsernameField: "username"}, nil
	}
	return CredentialPlan{Kind: CredentialKindNone}, nil
}

func bindSSHCredential(args map[string]any, binding CredentialBinding) (map[string]any, error) {
	out := cloneArgs(args)
	delete(out, "password")
	delete(out, "private_key")
	delete(out, "passphrase")
	out["credential_id"] = binding.ID
	switch binding.Type {
	case credential_entity.TypePassword:
		if authType := ArgString(out, "auth_type"); authType != "" && authType != asset_entity.AuthTypePassword {
			return nil, fmt.Errorf("password credential conflicts with auth_type %q", authType)
		}
		out["auth_type"] = asset_entity.AuthTypePassword
	case credential_entity.TypeSSHKey:
		if authType := ArgString(out, "auth_type"); authType != "" && authType != asset_entity.AuthTypeKey {
			return nil, fmt.Errorf("SSH key credential conflicts with auth_type %q", authType)
		}
		out["auth_type"] = asset_entity.AuthTypeKey
	default:
		return nil, fmt.Errorf("credential type %q is not accepted for SSH", binding.Type)
	}
	return out, nil
}

func (*databaseHandler) AutomationContract() AutomationContract {
	return newAutomationContract(
		[]string{"driver", "host", "port", "username", "password", "credential_id", "database", "read_only", "query_timeout_seconds", "ssh_asset_id", "sqlite_source", "path"},
		[]string{"driver", "host", "port", "username", "database", "read_only", "query_timeout_seconds", "ssh_asset_id", "sqlite_source", "path"},
		normalizeDatabaseAutomation,
		databaseCredentialPlan,
		bindDatabaseCredential,
	)
}

func normalizeDatabaseAutomation(args map[string]any) error {
	driver := asset_entity.DatabaseDriver(ArgString(args, "driver"))
	if driver == "" {
		return fmt.Errorf("database type requires driver parameter (mysql, postgresql, mssql, sqlite)")
	}
	if driver == asset_entity.DriverSQLite {
		if ArgString(args, "sqlite_source") == "" {
			args["sqlite_source"] = string(asset_entity.SQLiteSourceLocal)
		}
		return nil
	}
	if ArgInt(args, "port") == 0 {
		if port := driver.DefaultPort(); port > 0 {
			args["port"] = port
		}
	}
	return nil
}

func databaseCredentialPlan(args map[string]any) (CredentialPlan, error) {
	if asset_entity.DatabaseDriver(ArgString(args, "driver")) == asset_entity.DriverSQLite {
		return noCredentialPlan("password", "credential_id")(args)
	}
	return passwordCredentialPlan(args, "password", "username")
}

func bindDatabaseCredential(args map[string]any, binding CredentialBinding) (map[string]any, error) {
	if asset_entity.DatabaseDriver(ArgString(args, "driver")) == asset_entity.DriverSQLite {
		return nil, fmt.Errorf("credential_id is not applicable to SQLite")
	}
	return bindPasswordCredential(args, binding, "password")
}

func (*redisHandler) AutomationContract() AutomationContract {
	return passwordAutomationContract(
		[]string{"host", "port", "username", "password", "credential_id", "redis_db", "ssh_asset_id"},
		[]string{"host", "port", "username", "redis_db", "ssh_asset_id"},
		"username", normalizeDefaultPort(6379),
	)
}

func (*mongodbHandler) AutomationContract() AutomationContract {
	return passwordAutomationContract(
		[]string{"host", "port", "username", "password", "credential_id", "database", "ssh_asset_id"},
		[]string{"host", "port", "username", "database", "ssh_asset_id"},
		"username", normalizeDefaultPort(27017),
	)
}

func (*etcdHandler) AutomationContract() AutomationContract {
	return passwordAutomationContract(
		[]string{"endpoints", "username", "password", "credential_id", "ssh_asset_id", "tls", "tls_insecure", "tls_server_name", "tls_ca_file", "tls_cert_file", "tls_key_file", "dial_timeout_seconds", "command_timeout_seconds"},
		[]string{"endpoints", "username", "ssh_asset_id", "tls", "tls_insecure", "tls_server_name", "tls_ca_file", "tls_cert_file", "tls_key_file", "dial_timeout_seconds", "command_timeout_seconds"},
		"username", nil,
	)
}

func (*kafkaHandler) AutomationContract() AutomationContract {
	return passwordAutomationContract(
		[]string{"brokers", "host", "port", "client_id", "sasl_mechanism", "username", "password", "credential_id", "tls", "tls_insecure", "tls_server_name", "tls_ca_file", "tls_cert_file", "tls_key_file", "request_timeout_seconds", "message_preview_bytes", "message_fetch_limit", "ssh_asset_id"},
		[]string{"brokers", "host", "port", "client_id", "sasl_mechanism", "username", "tls", "tls_insecure", "tls_server_name", "tls_ca_file", "tls_cert_file", "tls_key_file", "request_timeout_seconds", "message_preview_bytes", "message_fetch_limit", "ssh_asset_id"},
		"username", normalizeKafkaAutomation,
	)
}

func normalizeKafkaAutomation(args map[string]any) error {
	if len(ArgStringSlice(args, "brokers")) == 0 && ArgString(args, "host") != "" {
		port := ArgInt(args, "port")
		if port == 0 {
			port = 9092
			args["port"] = port
		}
	}
	if ArgString(args, "sasl_mechanism") == "" {
		args["sasl_mechanism"] = asset_entity.KafkaSASLNone
	}
	return nil
}

func (*rdpHandler) AutomationContract() AutomationContract {
	return passwordAutomationContract(
		[]string{"host", "port", "username", "password", "credential_id", "domain", "width", "height", "clipboard", "ssh_asset_id"},
		[]string{"host", "port", "username", "domain", "width", "height", "clipboard", "ssh_asset_id"},
		"username", normalizeRDPAutomation,
	)
}

func normalizeRDPAutomation(args map[string]any) error {
	if ArgInt(args, "port") == 0 {
		args["port"] = 3389
	}
	if ArgInt(args, "width") == 0 {
		args["width"] = 1280
	}
	if ArgInt(args, "height") == 0 {
		args["height"] = 720
	}
	if _, ok := args["clipboard"]; !ok {
		args["clipboard"] = true
	}
	return nil
}

func (*vncHandler) AutomationContract() AutomationContract {
	return passwordAutomationContract(
		[]string{"host", "port", "username", "password", "credential_id", "file_ssh_asset_id"},
		[]string{"host", "port", "username", "file_ssh_asset_id"},
		"username", normalizeDefaultPort(5900),
	)
}

func (*ossHandler) AutomationContract() AutomationContract {
	return newAutomationContract(
		[]string{"provider", "endpoint", "region", "access_key_id", "secret_access_key", "credential_id", "use_path_style", "use_ssl", "connect_timeout"},
		[]string{"provider", "endpoint", "region", "access_key_id", "use_path_style", "use_ssl", "connect_timeout"},
		nil,
		func(args map[string]any) (CredentialPlan, error) {
			return passwordCredentialPlan(args, "secret_access_key", "access_key_id")
		},
		func(args map[string]any, binding CredentialBinding) (map[string]any, error) {
			return bindPasswordCredential(args, binding, "secret_access_key")
		},
	)
}

func (*k8sHandler) AutomationContract() AutomationContract {
	return newAutomationContract(
		[]string{"kubeconfig", "namespace", "context", "ssh_asset_id", "password", "credential_id"},
		[]string{"namespace", "context", "ssh_asset_id"},
		nil, noCredentialPlan("password", "credential_id"), nil,
	)
}

func (*serialHandler) AutomationContract() AutomationContract {
	return newAutomationContract(
		[]string{"port_path", "baud_rate", "data_bits", "stop_bits", "parity", "flow_control", "password", "credential_id"},
		[]string{"port_path", "baud_rate", "data_bits", "stop_bits", "parity", "flow_control"},
		normalizeSerialAutomation, noCredentialPlan("password", "credential_id"), nil,
	)
}

func normalizeSerialAutomation(args map[string]any) error {
	if ArgInt(args, "data_bits") == 0 {
		args["data_bits"] = 8
	}
	if ArgString(args, "stop_bits") == "" {
		args["stop_bits"] = "1"
	}
	if ArgString(args, "parity") == "" {
		args["parity"] = "none"
	}
	return nil
}

func (*localHandler) AutomationContract() AutomationContract {
	return newAutomationContract(
		[]string{"shell", "args", "cwd", "password", "credential_id"},
		[]string{"shell", "args", "cwd"},
		nil, noCredentialPlan("password", "credential_id"), nil,
	)
}
