package postgres

import (
	"encoding/json"
	"fmt"

	"github.com/openchat/openchat/server/store/types"
)

// CreateFeedbackReport inserts a user feedback report and returns its ID.
func (a *Adapter) CreateFeedbackReport(report *types.FeedbackReport) (int64, error) {
	if report == nil {
		return 0, fmt.Errorf("feedback report is nil")
	}

	var attachmentsJSON interface{}
	if len(report.Attachments) > 0 {
		raw, err := json.Marshal(report.Attachments)
		if err != nil {
			return 0, fmt.Errorf("marshal feedback attachments: %w", err)
		}
		attachmentsJSON = string(raw)
	}

	var id int64
	err := a.db.QueryRow(
		`INSERT INTO feedback_reports
		 (user_id, category, title, description, page_url, user_agent, attachments)
		 VALUES ($1, $2, $3, $4, $5, $6, CAST($7 AS jsonb))
		 RETURNING id`,
		report.UserID,
		report.Category,
		report.Title,
		report.Description,
		report.PageURL,
		report.UserAgent,
		attachmentsJSON,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create feedback report: %w", err)
	}
	return id, nil
}
