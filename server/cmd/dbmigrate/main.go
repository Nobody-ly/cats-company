package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/openchat/openchat/server/db/postgres"
)

type tablePlan struct {
	Name       string
	SourceSQL  string
	InsertSQL  string
	Copy       func(*sql.DB, *sql.DB) (int64, error)
	ResetSeq   string
	TargetName string
}

type tableReport struct {
	Name        string
	SourceCount int64
	TargetCount int64
	Status      string
}

func main() {
	var (
		mode        = flag.String("mode", getenv("CATS_MIGRATION_MODE", "report"), "report or dry-run-copy")
		mysqlDSN    = flag.String("mysql-dsn", getenv("CATS_MYSQL_DSN", ""), "source MySQL DSN")
		postgresDSN = flag.String("postgres-dsn", getenv("CATS_POSTGRES_DSN", getenv("OC_DB_DSN", "")), "target PostgreSQL URL DSN")
		schema      = flag.String("schema", getenv("CATS_MIGRATION_SCHEMA", ""), "target PostgreSQL schema for dry-run-copy")
		keepSchema  = flag.Bool("keep-schema", getenvBool("CATS_MIGRATION_KEEP_SCHEMA"), "keep dry-run PostgreSQL schema after copy")
	)
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if *mysqlDSN == "" {
		log.Fatal("missing -mysql-dsn or CATS_MYSQL_DSN")
	}
	if *postgresDSN == "" {
		log.Fatal("missing -postgres-dsn, CATS_POSTGRES_DSN, or OC_DB_DSN")
	}

	source, err := sql.Open("mysql", normalizeMySQLDSN(*mysqlDSN))
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer source.Close()
	if err := source.Ping(); err != nil {
		log.Fatalf("ping mysql: %v", err)
	}

	switch *mode {
	case "report":
		target, err := sql.Open("pgx", *postgresDSN)
		if err != nil {
			log.Fatalf("open postgres: %v", err)
		}
		defer target.Close()
		if err := target.Ping(); err != nil {
			log.Fatalf("ping postgres: %v", err)
		}
		reports := countTables(source, target, migrationPlans())
		printReport("migration report", reports)
	case "dry-run-copy":
		if *schema == "" {
			*schema = fmt.Sprintf("cats_migration_%s", time.Now().Format("20060102_150405"))
		}
		if err := validateSchemaName(*schema); err != nil {
			log.Fatalf("invalid schema: %v", err)
		}
		if err := runDryCopy(source, *postgresDSN, *schema, *keepSchema); err != nil {
			log.Fatalf("dry-run copy failed: %v", err)
		}
	default:
		log.Fatalf("unsupported mode %q", *mode)
	}
}

func runDryCopy(source *sql.DB, postgresDSN, schema string, keepSchema bool) error {
	admin, err := sql.Open("pgx", postgresDSN)
	if err != nil {
		return fmt.Errorf("open postgres admin connection: %w", err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	quotedSchema := quoteIdent(schema)
	if _, err := admin.Exec(`DROP SCHEMA IF EXISTS ` + quotedSchema + ` CASCADE`); err != nil {
		return fmt.Errorf("drop existing dry-run schema: %w", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + quotedSchema); err != nil {
		return fmt.Errorf("create dry-run schema: %w", err)
	}
	if !keepSchema {
		defer func() {
			if _, err := admin.Exec(`DROP SCHEMA IF EXISTS ` + quotedSchema + ` CASCADE`); err != nil {
				log.Printf("cleanup dry-run schema %s failed: %v", schema, err)
			}
		}()
	}

	schemaDSN, err := dsnWithSearchPath(postgresDSN, schema)
	if err != nil {
		return err
	}
	adapter := &postgres.Adapter{}
	if err := adapter.Open(schemaDSN); err != nil {
		return fmt.Errorf("open postgres schema connection: %w", err)
	}
	defer adapter.Close()
	if err := adapter.CreateSchema(); err != nil {
		return fmt.Errorf("create postgres schema: %w", err)
	}

	target, err := sql.Open("pgx", schemaDSN)
	if err != nil {
		return fmt.Errorf("open postgres copy connection: %w", err)
	}
	defer target.Close()

	var reports []tableReport
	for _, plan := range migrationPlans() {
		sourceCount, err := countRows(source, sourceTableName(plan.Name))
		if err != nil {
			return fmt.Errorf("count source %s: %w", plan.Name, err)
		}
		copied, err := plan.Copy(source, target)
		if err != nil {
			return fmt.Errorf("copy %s: %w", plan.Name, err)
		}
		if plan.ResetSeq != "" {
			if _, err := target.Exec(plan.ResetSeq); err != nil {
				return fmt.Errorf("reset sequence %s: %w", plan.Name, err)
			}
		}
		targetCount, err := countRows(target, targetTableName(plan))
		if err != nil {
			return fmt.Errorf("count target %s: %w", plan.Name, err)
		}
		status := "ok"
		if copied != sourceCount || targetCount != sourceCount {
			status = "mismatch"
		}
		reports = append(reports, tableReport{
			Name:        plan.Name,
			SourceCount: sourceCount,
			TargetCount: targetCount,
			Status:      status,
		})
	}

	printReport("dry-run copy report: "+schema, reports)
	if !keepSchema {
		log.Printf("dry-run schema %s will be removed; rerun with -keep-schema to inspect it", schema)
	}
	return nil
}

func migrationPlans() []tablePlan {
	return []tablePlan{
		{Name: "users", Copy: copyUsers, ResetSeq: resetSeqSQL("users", "id")},
		{Name: "rate_limits", Copy: copyRateLimits},
		{Name: "topics", Copy: copyTopics},
		{Name: "friends", Copy: copyFriends, ResetSeq: resetSeqSQL("friends", "id")},
		{Name: "groups", Copy: copyGroups, ResetSeq: resetSeqSQL(`"groups"`, "id"), TargetName: `"groups"`},
		{Name: "group_members", Copy: copyGroupMembers, ResetSeq: resetSeqSQL("group_members", "id")},
		{Name: "bot_config", Copy: copyBotConfig},
		{Name: "messages", Copy: copyMessages, ResetSeq: resetSeqSQL("messages", "id")},
		{Name: "feedback_reports", Copy: copyFeedbackReports, ResetSeq: resetSeqSQL("feedback_reports", "id")},
	}
}

func copyUsers(source, target *sql.DB) (int64, error) {
	rows, err := source.Query(`
		SELECT id, username, email, phone, display_name, avatar_url, account_type, pass_hash, state,
		       COALESCE(bot_disclose, 0), created_at, updated_at
		FROM users ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var copied int64
	for rows.Next() {
		var id int64
		var username, displayName, accountType string
		var email, phone, avatarURL sql.NullString
		var passHash []byte
		var state int
		var botDisclose bool
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &username, &email, &phone, &displayName, &avatarURL, &accountType, &passHash, &state, &botDisclose, &createdAt, &updatedAt); err != nil {
			return copied, err
		}
		if _, err := target.Exec(`
			INSERT INTO users (id, username, email, phone, display_name, avatar_url, account_type, pass_hash, state, bot_disclose, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			id, username, nullString(email), nullString(phone), displayName, nullString(avatarURL), accountType, passHash, state, botDisclose, createdAt, updatedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyRateLimits(source, target *sql.DB) (int64, error) {
	rows, err := source.Query(`SELECT account_type, max_per_second, max_per_minute, burst_size FROM rate_limits ORDER BY account_type`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var copied int64
	for rows.Next() {
		var accountType string
		var maxSecond, maxMinute, burst int
		if err := rows.Scan(&accountType, &maxSecond, &maxMinute, &burst); err != nil {
			return copied, err
		}
		if _, err := target.Exec(
			`INSERT INTO rate_limits (account_type, max_per_second, max_per_minute, burst_size) VALUES ($1,$2,$3,$4)`,
			accountType, maxSecond, maxMinute, burst,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyTopics(source, target *sql.DB) (int64, error) {
	rows, err := source.Query(`SELECT id, type, name, owner_id, created_at FROM topics ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var copied int64
	for rows.Next() {
		var id, topicType string
		var name sql.NullString
		var ownerID sql.NullInt64
		var createdAt time.Time
		if err := rows.Scan(&id, &topicType, &name, &ownerID, &createdAt); err != nil {
			return copied, err
		}
		if _, err := target.Exec(
			`INSERT INTO topics (id, type, name, owner_id, created_at) VALUES ($1,$2,$3,$4,$5)`,
			id, topicType, nullString(name), nullInt64(ownerID), createdAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyFriends(source, target *sql.DB) (int64, error) {
	rows, err := source.Query(`SELECT id, from_user_id, to_user_id, status, message, created_at, updated_at FROM friends ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var copied int64
	for rows.Next() {
		var id, fromUID, toUID int64
		var status string
		var message sql.NullString
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &fromUID, &toUID, &status, &message, &createdAt, &updatedAt); err != nil {
			return copied, err
		}
		if _, err := target.Exec(
			`INSERT INTO friends (id, from_user_id, to_user_id, status, message, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, fromUID, toUID, status, nullString(message), createdAt, updatedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyGroups(source, target *sql.DB) (int64, error) {
	rows, err := source.Query("SELECT id, name, owner_id, avatar_url, announcement, max_members, created_at FROM `groups` ORDER BY id")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var copied int64
	for rows.Next() {
		var id, ownerID int64
		var name string
		var avatarURL, announcement sql.NullString
		var maxMembers int
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &ownerID, &avatarURL, &announcement, &maxMembers, &createdAt); err != nil {
			return copied, err
		}
		if _, err := target.Exec(
			`INSERT INTO "groups" (id, name, owner_id, avatar_url, announcement, max_members, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, name, ownerID, nullString(avatarURL), nullString(announcement), maxMembers, createdAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyGroupMembers(source, target *sql.DB) (int64, error) {
	rows, err := source.Query(`SELECT id, group_id, user_id, role, COALESCE(muted, 0), joined_at FROM group_members ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var copied int64
	for rows.Next() {
		var id, groupID, userID int64
		var role string
		var muted bool
		var joinedAt time.Time
		if err := rows.Scan(&id, &groupID, &userID, &role, &muted, &joinedAt); err != nil {
			return copied, err
		}
		if _, err := target.Exec(
			`INSERT INTO group_members (id, group_id, user_id, role, muted, joined_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			id, groupID, userID, role, muted, joinedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyBotConfig(source, target *sql.DB) (int64, error) {
	rows, err := source.Query(`
		SELECT user_id, owner_id, api_endpoint, model, enabled, config, api_key, visibility, tenant_name, created_at, updated_at
		FROM bot_config ORDER BY user_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var copied int64
	for rows.Next() {
		var userID int64
		var ownerID sql.NullInt64
		var apiEndpoint, model, apiKey, visibility, tenantName, configJSON sql.NullString
		var enabled bool
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&userID, &ownerID, &apiEndpoint, &model, &enabled, &configJSON, &apiKey, &visibility, &tenantName, &createdAt, &updatedAt); err != nil {
			return copied, err
		}
		if _, err := target.Exec(
			`INSERT INTO bot_config (user_id, owner_id, api_endpoint, model, enabled, config, api_key, visibility, tenant_name, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,CAST($6 AS jsonb),$7,$8,$9,$10,$11)`,
			userID, nullInt64(ownerID), nullString(apiEndpoint), nullString(model), enabled, nullString(configJSON), nullString(apiKey), nullStringWithDefault(visibility, "public"), nullString(tenantName), createdAt, updatedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyMessages(source, target *sql.DB) (int64, error) {
	rows, err := source.Query(`
		SELECT id, topic_id, from_uid, content, msg_type, created_at, content_blocks, mode, role, reply_to
		FROM messages ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var copied int64
	for rows.Next() {
		var id, fromUID int64
		var topicID, content, msgType string
		var createdAt time.Time
		var blocksJSON, mode, role sql.NullString
		var replyTo sql.NullInt64
		if err := rows.Scan(&id, &topicID, &fromUID, &content, &msgType, &createdAt, &blocksJSON, &mode, &role, &replyTo); err != nil {
			return copied, err
		}
		if _, err := target.Exec(
			`INSERT INTO messages (id, topic_id, from_uid, content, msg_type, created_at, content_blocks, mode, role, reply_to)
			VALUES ($1,$2,$3,$4,$5,$6,CAST($7 AS jsonb),$8,$9,$10)`,
			id, topicID, fromUID, content, msgType, createdAt, nullString(blocksJSON), nullStringWithDefault(mode, "normal"), nullString(role), nullInt64(replyTo),
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyFeedbackReports(source, target *sql.DB) (int64, error) {
	rows, err := source.Query(`
		SELECT id, user_id, category, title, description, page_url, user_agent, status, attachments, created_at, updated_at
		FROM feedback_reports ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var copied int64
	for rows.Next() {
		var id, userID int64
		var category, description, status string
		var title, pageURL, userAgent, attachments sql.NullString
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &userID, &category, &title, &description, &pageURL, &userAgent, &status, &attachments, &createdAt, &updatedAt); err != nil {
			return copied, err
		}
		if _, err := target.Exec(
			`INSERT INTO feedback_reports (id, user_id, category, title, description, page_url, user_agent, status, attachments, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,CAST($9 AS jsonb),$10,$11)`,
			id, userID, category, nullString(title), description, nullString(pageURL), nullString(userAgent), status, nullString(attachments), createdAt, updatedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func countTables(source, target *sql.DB, plans []tablePlan) []tableReport {
	reports := make([]tableReport, 0, len(plans))
	for _, plan := range plans {
		report := tableReport{Name: plan.Name, Status: "ok"}
		sourceCount, err := countRows(source, sourceTableName(plan.Name))
		if err != nil {
			report.Status = "source_count_error: " + err.Error()
		} else {
			report.SourceCount = sourceCount
		}
		targetCount, err := countRows(target, targetTableName(plan))
		if err != nil {
			if report.Status == "ok" {
				report.Status = "target_count_error: " + err.Error()
			} else {
				report.Status += "; target_count_error: " + err.Error()
			}
		} else {
			report.TargetCount = targetCount
		}
		reports = append(reports, report)
	}
	return reports
}

func countRows(db *sql.DB, table string) (int64, error) {
	var count int64
	err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count)
	return count, err
}

func printReport(title string, reports []tableReport) {
	fmt.Println(title)
	fmt.Println("table,source_count,target_count,status")
	var hasMismatch bool
	for _, report := range reports {
		if report.Status != "ok" {
			hasMismatch = true
		}
		fmt.Printf("%s,%d,%d,%s\n", report.Name, report.SourceCount, report.TargetCount, report.Status)
	}
	if hasMismatch {
		os.Exit(2)
	}
}

func sourceTableName(name string) string {
	if name == "groups" {
		return "`groups`"
	}
	return name
}

func targetTableName(plan tablePlan) string {
	if plan.TargetName != "" {
		return plan.TargetName
	}
	return plan.Name
}

func resetSeqSQL(table, column string) string {
	relation := strings.ReplaceAll(table, `'`, `''`)
	return fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', '%s'), COALESCE((SELECT MAX(%s) FROM %s), 1), (SELECT COUNT(*) > 0 FROM %s))`, relation, column, column, table, table)
}

func nullString(value sql.NullString) interface{} {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullStringWithDefault(value sql.NullString, fallback string) string {
	if !value.Valid || value.String == "" {
		return fallback
	}
	return value.String
}

func nullInt64(value sql.NullInt64) interface{} {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func getenv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func getenvBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func normalizeMySQLDSN(rawDSN string) string {
	cfg, err := mysqlDriver.ParseDSN(rawDSN)
	if err != nil {
		return rawDSN
	}
	cfg.ParseTime = true
	if cfg.Collation == "" {
		cfg.Collation = "utf8mb4_unicode_ci"
	}
	return cfg.FormatDSN()
}

func validateSchemaName(schema string) error {
	if schema == "" {
		return errors.New("schema is empty")
	}
	if len(schema) > 63 {
		return errors.New("schema name is longer than PostgreSQL identifier limit")
	}
	for i, r := range schema {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			if i == 0 && r >= '0' && r <= '9' {
				return errors.New("schema must not start with a digit")
			}
			continue
		}
		return fmt.Errorf("schema contains unsupported character %q", r)
	}
	return nil
}

func dsnWithSearchPath(rawDSN, schema string) (string, error) {
	parsed, err := url.Parse(rawDSN)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("postgres DSN must be a URL DSN: %w", err)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
