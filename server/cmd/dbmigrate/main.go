package main

import (
	"database/sql"
	"encoding/json"
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
	Copy       func(*sql.DB, dbExecer) (int64, error)
	ResetSeq   string
	TargetName string
}

type tableReport struct {
	Name        string
	SourceCount int64
	TargetCount int64
	Status      string
}

type preflightIssue struct {
	Check    string
	Count    int64
	Samples  string
	Blocking bool
}

type dbExecer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

type dbQueryer interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

func main() {
	var (
		mode        = flag.String("mode", getenv("CATS_MIGRATION_MODE", "report"), "report or dry-run-copy")
		mysqlDSN    = flag.String("mysql-dsn", getenv("CATS_MYSQL_DSN", ""), "source MySQL DSN")
		postgresDSN = flag.String("postgres-dsn", getenv("CATS_POSTGRES_DSN", getenv("OC_DB_DSN", "")), "target PostgreSQL URL DSN")
		schema      = flag.String("schema", getenv("CATS_MIGRATION_SCHEMA", ""), "target PostgreSQL schema for report or dry-run-copy")
		keepSchema  = flag.Bool("keep-schema", getenvBool("CATS_MIGRATION_KEEP_SCHEMA"), "keep dry-run PostgreSQL schema after copy")
		confirmDrop = flag.String("confirm-drop-schema", getenv("CATS_MIGRATION_CONFIRM_DROP_SCHEMA", ""), "schema name required before dropping an existing migration schema")
		allowLossy  = flag.Bool("allow-lossy-cleanup", getenvBool("CATS_MIGRATION_ALLOW_LOSSY_CLEANUP"), "allow migration to clean NUL/invalid JSON and skip rows with invalid foreign keys")
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
		targetDSN := *postgresDSN
		if *schema != "" {
			if err := validateSchemaName(*schema); err != nil {
				log.Fatalf("invalid schema: %v", err)
			}
			targetDSN, err = dsnWithSearchPath(*postgresDSN, *schema)
			if err != nil {
				log.Fatal(err)
			}
		}
		target, err := sql.Open("pgx", targetDSN)
		if err != nil {
			log.Fatalf("open postgres: %v", err)
		}
		defer target.Close()
		if err := target.Ping(); err != nil {
			log.Fatalf("ping postgres: %v", err)
		}
		preflightIssues, fatalPreflight := runPreflightChecks(source, *allowLossy)
		reports := countTables(source, target, migrationPlans())
		printPreflightIssues(preflightIssues)
		printReport("migration report", reports, fatalPreflight)
	case "dry-run-copy":
		if *schema == "" {
			*schema = fmt.Sprintf("cats_migration_%s", time.Now().Format("20060102_150405"))
		}
		if err := validateSchemaName(*schema); err != nil {
			log.Fatalf("invalid schema: %v", err)
		}
		if err := runDryCopy(source, *postgresDSN, *schema, *keepSchema, *confirmDrop, *allowLossy); err != nil {
			log.Fatalf("dry-run copy failed: %v", err)
		}
	default:
		log.Fatalf("unsupported mode %q", *mode)
	}
}

func runDryCopy(source *sql.DB, postgresDSN, schema string, keepSchema bool, confirmDrop string, allowLossy bool) error {
	admin, err := sql.Open("pgx", postgresDSN)
	if err != nil {
		return fmt.Errorf("open postgres admin connection: %w", err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	quotedSchema := quoteIdent(schema)
	exists, err := schemaExists(admin, schema)
	if err != nil {
		return err
	}
	if exists {
		if confirmDrop != schema {
			return fmt.Errorf("schema %q already exists; pass -confirm-drop-schema=%s to drop it", schema, schema)
		}
		if _, err := admin.Exec(`DROP SCHEMA IF EXISTS ` + quotedSchema + ` CASCADE`); err != nil {
			return fmt.Errorf("drop existing dry-run schema: %w", err)
		}
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

	preflightIssues, fatalPreflight := runPreflightChecks(source, allowLossy)
	printPreflightIssues(preflightIssues)
	if fatalPreflight {
		return errors.New("source preflight checks failed")
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

	tx, err := target.Begin()
	if err != nil {
		return fmt.Errorf("begin postgres copy transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Printf("rollback dry-run transaction failed: %v", err)
		}
	}()

	var reports []tableReport
	for _, plan := range migrationPlans() {
		sourceCount, err := countRows(source, sourceTableName(plan.Name))
		if err != nil {
			return fmt.Errorf("count source %s: %w", plan.Name, err)
		}
		copied, err := plan.Copy(source, tx)
		if err != nil {
			return fmt.Errorf("copy %s: %w", plan.Name, err)
		}
		if plan.ResetSeq != "" {
			if _, err := tx.Exec(plan.ResetSeq); err != nil {
				return fmt.Errorf("reset sequence %s: %w", plan.Name, err)
			}
		}
		targetCount, err := countRows(tx, targetTableName(plan))
		if err != nil {
			return fmt.Errorf("count target %s: %w", plan.Name, err)
		}
		status := "ok"
		if copied == targetCount && copied < sourceCount {
			status = fmt.Sprintf("skipped_%d_invalid_refs", sourceCount-copied)
		} else if copied != sourceCount || targetCount != sourceCount {
			status = "mismatch"
		}
		reports = append(reports, tableReport{
			Name:        plan.Name,
			SourceCount: sourceCount,
			TargetCount: targetCount,
			Status:      status,
		})
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dry-run transaction: %w", err)
	}

	printReport("dry-run copy report: "+schema, reports, false)
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

func copyUsers(source *sql.DB, target dbExecer) (int64, error) {
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
			id, cleanText(username), nullText(email), nullText(phone), cleanText(displayName), nullText(avatarURL), cleanText(accountType), passHash, state, botDisclose, createdAt, updatedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyRateLimits(source *sql.DB, target dbExecer) (int64, error) {
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
			cleanText(accountType), maxSecond, maxMinute, burst,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyTopics(source *sql.DB, target dbExecer) (int64, error) {
	rows, err := source.Query(`
		SELECT t.id, t.type, t.name,
		       CASE WHEN t.owner_id IS NULL OR u.id IS NOT NULL THEN t.owner_id ELSE NULL END AS owner_id,
		       t.created_at
		FROM topics t
		LEFT JOIN users u ON u.id = t.owner_id
		ORDER BY t.id`)
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
			cleanText(id), cleanText(topicType), nullText(name), nullInt64(ownerID), createdAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyFriends(source *sql.DB, target dbExecer) (int64, error) {
	rows, err := source.Query(`
		SELECT f.id, f.from_user_id, f.to_user_id, f.status, f.message, f.created_at, f.updated_at
		FROM friends f
		INNER JOIN users from_user ON from_user.id = f.from_user_id
		INNER JOIN users to_user ON to_user.id = f.to_user_id
		ORDER BY f.id`)
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
			id, fromUID, toUID, cleanText(status), nullText(message), createdAt, updatedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyGroups(source *sql.DB, target dbExecer) (int64, error) {
	rows, err := source.Query(`
		SELECT g.id, g.name, g.owner_id, g.avatar_url, g.announcement, g.max_members, g.created_at
		FROM ` + "`groups`" + ` g
		INNER JOIN users owner_user ON owner_user.id = g.owner_id
		ORDER BY g.id`)
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
			id, cleanText(name), ownerID, nullText(avatarURL), nullText(announcement), maxMembers, createdAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyGroupMembers(source *sql.DB, target dbExecer) (int64, error) {
	rows, err := source.Query(`
		SELECT gm.id, gm.group_id, gm.user_id, gm.role, COALESCE(gm.muted, 0), gm.joined_at
		FROM group_members gm
		INNER JOIN ` + "`groups`" + ` g ON g.id = gm.group_id
		INNER JOIN users u ON u.id = gm.user_id
		ORDER BY gm.id`)
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
			id, groupID, userID, cleanText(role), muted, joinedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyBotConfig(source *sql.DB, target dbExecer) (int64, error) {
	rows, err := source.Query(`
		SELECT b.user_id,
		       CASE WHEN b.owner_id IS NULL OR owner_user.id IS NOT NULL THEN b.owner_id ELSE NULL END AS owner_id,
		       b.api_endpoint, b.model, b.enabled, b.config, b.api_key, b.visibility, b.tenant_name, b.created_at, b.updated_at
		FROM bot_config b
		INNER JOIN users bot_user ON bot_user.id = b.user_id
		LEFT JOIN users owner_user ON owner_user.id = b.owner_id
		ORDER BY b.user_id`)
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
			userID, nullInt64(ownerID), nullText(apiEndpoint), nullText(model), enabled, nullJSON(configJSON), nullText(apiKey), nullTextWithDefault(visibility, "public"), nullText(tenantName), createdAt, updatedAt,
		); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, rows.Err()
}

func copyMessages(source *sql.DB, target dbExecer) (int64, error) {
	rows, err := source.Query(`
		SELECT m.id, m.topic_id, m.from_uid, m.content, m.msg_type, m.created_at, m.content_blocks, m.mode, m.role, m.reply_to
		FROM messages m
		INNER JOIN topics t ON t.id = m.topic_id
		INNER JOIN users u ON u.id = m.from_uid
		ORDER BY m.id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	items := make([]messageCopyRow, 0, 65536)
	for rows.Next() {
		var item messageCopyRow
		if err := rows.Scan(&item.id, &item.topicID, &item.fromUID, &item.content, &item.msgType, &item.createdAt, &item.blocksJSON, &item.mode, &item.role, &item.replyTo); err != nil {
			return 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var copied int64
	for len(items) > 0 {
		batchSize := 500
		if len(items) < batchSize {
			batchSize = len(items)
		}
		batch := items[:batchSize]
		n, err := flushMessagesBatch(target, batch)
		if err != nil {
			return copied, err
		}
		copied += n
		items = items[batchSize:]
	}
	return copied, nil
}

type messageCopyRow struct {
	id         int64
	topicID    string
	fromUID    int64
	content    string
	msgType    string
	createdAt  time.Time
	blocksJSON sql.NullString
	mode       sql.NullString
	role       sql.NullString
	replyTo    sql.NullInt64
}

func flushMessagesBatch(target dbExecer, batch []messageCopyRow) (int64, error) {
	if len(batch) == 0 {
		return 0, nil
	}
	var builder strings.Builder
	args := make([]interface{}, 0, len(batch)*10)
	builder.WriteString(`INSERT INTO messages (id, topic_id, from_uid, content, msg_type, created_at, content_blocks, mode, role, reply_to) VALUES `)
	for i, item := range batch {
		if i > 0 {
			builder.WriteByte(',')
		}
		base := i*10 + 1
		builder.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,CAST($%d AS jsonb),$%d,$%d,$%d)",
			base, base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9))
		args = append(args,
			item.id,
			cleanText(item.topicID),
			item.fromUID,
			cleanText(item.content),
			cleanText(item.msgType),
			item.createdAt,
			nullJSON(item.blocksJSON),
			nullTextWithDefault(item.mode, "normal"),
			nullText(item.role),
			nullInt64(item.replyTo),
		)
	}
	if _, err := target.Exec(builder.String(), args...); err != nil {
		return 0, err
	}
	return int64(len(batch)), nil
}

func copyFeedbackReports(source *sql.DB, target dbExecer) (int64, error) {
	rows, err := source.Query(`
		SELECT f.id, f.user_id, f.category, f.title, f.description, f.page_url, f.user_agent, f.status, f.attachments, f.created_at, f.updated_at
		FROM feedback_reports f
		INNER JOIN users u ON u.id = f.user_id
		ORDER BY f.id`)
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
			id, userID, cleanText(category), nullText(title), cleanText(description), nullText(pageURL), nullText(userAgent), cleanText(status), nullJSON(attachments), createdAt, updatedAt,
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
		if report.Status == "ok" && report.SourceCount != report.TargetCount {
			report.Status = "mismatch"
		}
		reports = append(reports, report)
	}
	return reports
}

func countRows(db dbQueryer, table string) (int64, error) {
	var count int64
	err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count)
	return count, err
}

func printReport(title string, reports []tableReport, fatalPreflight bool) {
	fmt.Println(title)
	fmt.Println("table,source_count,target_count,status")
	hasMismatch := fatalPreflight
	for _, report := range reports {
		if !strings.HasPrefix(report.Status, "ok") {
			hasMismatch = true
		}
		fmt.Printf("%s,%d,%d,%s\n", report.Name, report.SourceCount, report.TargetCount, report.Status)
	}
	if hasMismatch {
		os.Exit(2)
	}
}

func printPreflightIssues(issues []preflightIssue) {
	if len(issues) == 0 {
		fmt.Println("source preflight checks: ok")
		return
	}
	fmt.Println("source preflight issues")
	fmt.Println("check,count,blocking,samples")
	for _, issue := range issues {
		fmt.Printf("%s,%d,%t,%s\n", issue.Check, issue.Count, issue.Blocking, csvSafe(issue.Samples))
	}
}

func runPreflightChecks(source *sql.DB, allowLossy bool) ([]preflightIssue, bool) {
	checks := []struct {
		name      string
		countSQL  string
		sampleSQL string
		lossy     bool
	}{
		{
			name: "duplicate_user_email",
			countSQL: `SELECT COUNT(*) FROM (
				SELECT LOWER(email) AS value FROM users
				WHERE email IS NOT NULL AND email <> ''
				GROUP BY LOWER(email) HAVING COUNT(*) > 1
			) dup`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(CONCAT('email_hash:', SHA2(value, 256)) ORDER BY value SEPARATOR '; '), '') FROM (
				SELECT LOWER(email) AS value FROM users
				WHERE email IS NOT NULL AND email <> ''
				GROUP BY LOWER(email) HAVING COUNT(*) > 1
				LIMIT 10
			) dup`,
		},
		{
			name: "duplicate_bot_api_key",
			countSQL: `SELECT COUNT(*) FROM (
				SELECT api_key AS value FROM bot_config
				WHERE api_key IS NOT NULL AND api_key <> ''
				GROUP BY api_key HAVING COUNT(*) > 1
			) dup`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(CONCAT('api_key_hash:', SHA2(value, 256)) ORDER BY value SEPARATOR '; '), '') FROM (
				SELECT api_key AS value FROM bot_config
				WHERE api_key IS NOT NULL AND api_key <> ''
				GROUP BY api_key HAVING COUNT(*) > 1
				LIMIT 10
			) dup`,
		},
		{
			name: "orphan_friends",
			countSQL: `SELECT COUNT(*) FROM friends f
				LEFT JOIN users from_user ON from_user.id = f.from_user_id
				LEFT JOIN users to_user ON to_user.id = f.to_user_id
				WHERE from_user.id IS NULL OR to_user.id IS NULL`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(id ORDER BY id SEPARATOR '; '), '') FROM (
				SELECT f.id FROM friends f
				LEFT JOIN users from_user ON from_user.id = f.from_user_id
				LEFT JOIN users to_user ON to_user.id = f.to_user_id
				WHERE from_user.id IS NULL OR to_user.id IS NULL
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "orphan_groups",
			countSQL: `SELECT COUNT(*) FROM ` + "`groups`" + ` g
				LEFT JOIN users owner_user ON owner_user.id = g.owner_id
				WHERE owner_user.id IS NULL`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(id ORDER BY id SEPARATOR '; '), '') FROM (
				SELECT g.id FROM ` + "`groups`" + ` g
				LEFT JOIN users owner_user ON owner_user.id = g.owner_id
				WHERE owner_user.id IS NULL
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "orphan_group_members",
			countSQL: `SELECT COUNT(*) FROM group_members gm
				LEFT JOIN ` + "`groups`" + ` g ON g.id = gm.group_id
				LEFT JOIN users u ON u.id = gm.user_id
				WHERE g.id IS NULL OR u.id IS NULL`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(id ORDER BY id SEPARATOR '; '), '') FROM (
				SELECT gm.id FROM group_members gm
				LEFT JOIN ` + "`groups`" + ` g ON g.id = gm.group_id
				LEFT JOIN users u ON u.id = gm.user_id
				WHERE g.id IS NULL OR u.id IS NULL
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "orphan_bot_config",
			countSQL: `SELECT COUNT(*) FROM bot_config b
				LEFT JOIN users bot_user ON bot_user.id = b.user_id
				WHERE bot_user.id IS NULL`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(user_id ORDER BY user_id SEPARATOR '; '), '') FROM (
				SELECT b.user_id FROM bot_config b
				LEFT JOIN users bot_user ON bot_user.id = b.user_id
				WHERE bot_user.id IS NULL
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "orphan_messages",
			countSQL: `SELECT COUNT(*) FROM messages m
				LEFT JOIN topics t ON t.id = m.topic_id
				LEFT JOIN users u ON u.id = m.from_uid
				WHERE t.id IS NULL OR u.id IS NULL`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(id ORDER BY id SEPARATOR '; '), '') FROM (
				SELECT m.id FROM messages m
				LEFT JOIN topics t ON t.id = m.topic_id
				LEFT JOIN users u ON u.id = m.from_uid
				WHERE t.id IS NULL OR u.id IS NULL
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "orphan_feedback_reports",
			countSQL: `SELECT COUNT(*) FROM feedback_reports f
				LEFT JOIN users u ON u.id = f.user_id
				WHERE u.id IS NULL`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(id ORDER BY id SEPARATOR '; '), '') FROM (
				SELECT f.id FROM feedback_reports f
				LEFT JOIN users u ON u.id = f.user_id
				WHERE u.id IS NULL
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "invalid_bot_config_json",
			countSQL: `SELECT COUNT(*) FROM bot_config
				WHERE config IS NOT NULL AND config <> '' AND JSON_VALID(REPLACE(REPLACE(config, CHAR(0), ''), '\\u0000', '')) = 0`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(user_id ORDER BY user_id SEPARATOR '; '), '') FROM (
				SELECT user_id FROM bot_config
				WHERE config IS NOT NULL AND config <> '' AND JSON_VALID(REPLACE(REPLACE(config, CHAR(0), ''), '\\u0000', '')) = 0
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "invalid_message_content_blocks_json",
			countSQL: `SELECT COUNT(*) FROM messages
				WHERE content_blocks IS NOT NULL AND content_blocks <> '' AND JSON_VALID(REPLACE(REPLACE(content_blocks, CHAR(0), ''), '\\u0000', '')) = 0`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(id ORDER BY id SEPARATOR '; '), '') FROM (
				SELECT id FROM messages
				WHERE content_blocks IS NOT NULL AND content_blocks <> '' AND JSON_VALID(REPLACE(REPLACE(content_blocks, CHAR(0), ''), '\\u0000', '')) = 0
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "invalid_feedback_attachments_json",
			countSQL: `SELECT COUNT(*) FROM feedback_reports
				WHERE attachments IS NOT NULL AND attachments <> '' AND JSON_VALID(REPLACE(REPLACE(attachments, CHAR(0), ''), '\\u0000', '')) = 0`,
			sampleSQL: `SELECT COALESCE(GROUP_CONCAT(id ORDER BY id SEPARATOR '; '), '') FROM (
				SELECT id FROM feedback_reports
				WHERE attachments IS NOT NULL AND attachments <> '' AND JSON_VALID(REPLACE(REPLACE(attachments, CHAR(0), ''), '\\u0000', '')) = 0
				LIMIT 10
			) bad`,
			lossy: true,
		},
		{
			name: "text_nul_bytes",
			countSQL: `SELECT
				(SELECT COUNT(*) FROM users WHERE username LIKE CONCAT('%', CHAR(0), '%') OR display_name LIKE CONCAT('%', CHAR(0), '%') OR COALESCE(email, '') LIKE CONCAT('%', CHAR(0), '%') OR COALESCE(phone, '') LIKE CONCAT('%', CHAR(0), '%') OR COALESCE(avatar_url, '') LIKE CONCAT('%', CHAR(0), '%')) +
				(SELECT COUNT(*) FROM topics WHERE id LIKE CONCAT('%', CHAR(0), '%') OR type LIKE CONCAT('%', CHAR(0), '%') OR COALESCE(name, '') LIKE CONCAT('%', CHAR(0), '%')) +
				(SELECT COUNT(*) FROM messages WHERE topic_id LIKE CONCAT('%', CHAR(0), '%') OR content LIKE CONCAT('%', CHAR(0), '%') OR msg_type LIKE CONCAT('%', CHAR(0), '%') OR COALESCE(mode, '') LIKE CONCAT('%', CHAR(0), '%') OR COALESCE(role, '') LIKE CONCAT('%', CHAR(0), '%'))`,
			sampleSQL: `SELECT 'see count'`,
			lossy:     true,
		},
	}

	var issues []preflightIssue
	var fatal bool
	for _, check := range checks {
		count, samples, err := countPreflightIssue(source, check.countSQL, check.sampleSQL)
		if err != nil {
			issues = append(issues, preflightIssue{Check: check.name + "_check_error", Count: 1, Samples: err.Error(), Blocking: true})
			fatal = true
			continue
		}
		if count == 0 {
			continue
		}
		blocking := !check.lossy || !allowLossy
		issues = append(issues, preflightIssue{Check: check.name, Count: count, Samples: samples, Blocking: blocking})
		if blocking {
			fatal = true
		}
	}
	return issues, fatal
}

func countPreflightIssue(db *sql.DB, countSQL, sampleSQL string) (int64, string, error) {
	var count int64
	if err := db.QueryRow(countSQL).Scan(&count); err != nil {
		return 0, "", err
	}
	if count == 0 {
		return 0, "", nil
	}
	var samples sql.NullString
	if err := db.QueryRow(sampleSQL).Scan(&samples); err != nil {
		return count, "", err
	}
	return count, samples.String, nil
}

func schemaExists(db *sql.DB, schema string) (bool, error) {
	var exists bool
	err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`, schema).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check schema exists: %w", err)
	}
	return exists, nil
}

func csvSafe(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	if strings.ContainsAny(value, ",\"") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
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

func cleanText(value string) string {
	return strings.ReplaceAll(value, "\x00", "")
}

func nullText(value sql.NullString) interface{} {
	if !value.Valid {
		return nil
	}
	return cleanText(value.String)
}

func nullTextWithDefault(value sql.NullString, fallback string) string {
	if !value.Valid || value.String == "" {
		return fallback
	}
	return cleanText(value.String)
}

func nullJSON(value sql.NullString) interface{} {
	if !value.Valid {
		return nil
	}
	cleaned := strings.TrimSpace(cleanText(value.String))
	cleaned = strings.ReplaceAll(cleaned, `\u0000`, "")
	if cleaned == "" {
		return nil
	}
	if !json.Valid([]byte(cleaned)) {
		return nil
	}
	return cleaned
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
	lower := strings.ToLower(schema)
	if lower == "public" || strings.HasPrefix(lower, "pg_") || lower == "information_schema" {
		return errors.New("schema must not be a PostgreSQL system or application default schema")
	}
	if !strings.HasPrefix(lower, "cats_migration_") && !strings.HasPrefix(lower, "cats_shadow_") {
		return errors.New("schema must start with cats_migration_ or cats_shadow_")
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
