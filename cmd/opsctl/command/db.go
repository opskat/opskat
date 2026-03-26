package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/opskat/opskat/internal/ai"
	"github.com/opskat/opskat/internal/approval"
)

func cmdSQL(ctx context.Context, handlers map[string]ai.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printSQLUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}

	asset, err := resolveAsset(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fs := flag.NewFlagSet("sql", flag.ContinueOnError)
	file := fs.String("f", "", "Read SQL from file")
	database := fs.String("d", "", "Override default database")
	fs.Usage = func() { printSQLUsage() }
	_ = fs.Parse(args[1:])

	var sqlText string
	if *file != "" {
		data, readErr := os.ReadFile(*file)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", readErr)
			return 1
		}
		sqlText = string(data)
	} else {
		sqlText = strings.Join(fs.Args(), " ")
	}

	if sqlText == "" {
		fmt.Fprintln(os.Stderr, "Error: SQL statement is required")
		printSQLUsage()
		return 1
	}

	// Require approval
	argsJSON := fmt.Sprintf(`{"asset_id":%d,"sql":%q}`, asset.ID, truncateStr(sqlText, 200))
	approvalResult, approvalErr := requireApproval(ctx, approval.ApprovalRequest{
		Type:      "sql",
		AssetID:   asset.ID,
		AssetName: asset.Name,
		Command:   sqlText,
		Detail:    fmt.Sprintf("opsctl sql %s %q", args[0], truncateStr(sqlText, 100)),
		SessionID: session,
	})
	auditCtx := ai.WithSessionID(ctx, approvalResult.SessionID)
	if approvalErr != nil {
		writeOpsctlAudit(auditCtx, "exec_sql", argsJSON, "", approvalErr, approvalResult.ToCheckResult())
		fmt.Fprintf(os.Stderr, "Error: %v\n", approvalErr)
		return 1
	}

	params := map[string]any{
		"asset_id": float64(asset.ID),
		"sql":      sqlText,
	}
	if *database != "" {
		params["database"] = *database
	}
	return callHandler(auditCtx, handlers, "exec_sql", params)
}

func cmdRedisCmd(ctx context.Context, handlers map[string]ai.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printRedisUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}

	asset, err := resolveAsset(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fs := flag.NewFlagSet("redis", flag.ContinueOnError)
	db := fs.Int("n", -1, "Redis database number (0-15)")
	fs.Usage = func() { printRedisUsage() }
	_ = fs.Parse(args[1:])

	command := strings.Join(fs.Args(), " ")
	if command == "" {
		fmt.Fprintln(os.Stderr, "Error: Redis command is required")
		printRedisUsage()
		return 1
	}

	// Require approval
	argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, asset.ID, truncateStr(command, 200))
	approvalResult, approvalErr := requireApproval(ctx, approval.ApprovalRequest{
		Type:      "redis",
		AssetID:   asset.ID,
		AssetName: asset.Name,
		Command:   command,
		Detail:    fmt.Sprintf("opsctl redis %s %q", args[0], truncateStr(command, 100)),
		SessionID: session,
	})
	auditCtx := ai.WithSessionID(ctx, approvalResult.SessionID)
	if approvalErr != nil {
		writeOpsctlAudit(auditCtx, "exec_redis", argsJSON, "", approvalErr, approvalResult.ToCheckResult())
		fmt.Fprintf(os.Stderr, "Error: %v\n", approvalErr)
		return 1
	}

	params := map[string]any{
		"asset_id": float64(asset.ID),
		"command":  command,
	}
	if *db >= 0 {
		params["db"] = float64(*db)
	}
	return callHandler(auditCtx, handlers, "exec_redis", params)
}

func printSQLUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl [--session <id>] sql <asset> [flags] "<SQL>"

Arguments:
  asset     Database asset name or numeric ID

Flags:
  -f <file>     Read SQL from file instead of argument
  -d <database> Override the default database for this execution

Approval:
  SQL statements are checked against the asset's query policy:
  - Allowed types (e.g. SELECT) execute without approval
  - Denied types (e.g. DROP TABLE) are rejected
  - Other statements require user confirmation (desktop app) or are rejected (offline)

Examples:
  opsctl sql prod-db "SELECT * FROM users LIMIT 10"
  opsctl sql prod-db "INSERT INTO logs (msg) VALUES ('test')"
  opsctl sql prod-db -f migration.sql
  opsctl sql prod-db -d other_db "SHOW TABLES"
`)
}

func printRedisUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl [--session <id>] redis <asset> [flags] "<command>"

Arguments:
  asset     Redis asset name or numeric ID
  command   Redis command (e.g. "GET mykey", "HGETALL user:1")

Flags:
  -n <db>   Override the default database number (0-15)

Approval:
  Commands are checked against the asset's Redis policy:
  - Dangerous commands (FLUSHDB, CONFIG SET, etc.) are rejected by default
  - Other commands require user confirmation (desktop app) or are rejected (offline)

Examples:
  opsctl redis cache "GET session:abc123"
  opsctl redis cache "HGETALL user:1"
  opsctl redis cache -n 2 "KEYS user:*"
  opsctl redis cache "SET key value EX 3600"
`)
}
