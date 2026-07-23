package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/pkg/auditredact"
)

// migration202607230001 removes credential plaintext from audit rows written
// before audit redaction became mandatory. The migration is intentionally
// irreversible: rollback must never restore secrets.
func migration202607230001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202607230001",
		Migrate: func(tx *gorm.DB) error {
			type auditPayload struct {
				ID      int64
				Request string
				Result  string
				Command string
				Error   string
			}
			var lastID int64
			for {
				var batch []auditPayload
				if err := tx.Table("audit_logs").Select("id, request, result, command, error").
					Where("id > ?", lastID).Order("id").Limit(100).Find(&batch).Error; err != nil {
					return err
				}
				if len(batch) == 0 {
					return nil
				}
				for _, row := range batch {
					updates := map[string]any{
						"request": auditredact.JSON(row.Request),
						"result":  auditredact.Result(row.Result),
						"command": auditredact.Text(row.Command),
						"error":   auditredact.Text(row.Error),
					}
					if err := tx.Table("audit_logs").Where("id = ?", row.ID).Updates(updates).Error; err != nil {
						return err
					}
					lastID = row.ID
				}
			}
		},
		Rollback: func(_ *gorm.DB) error { return nil },
	}
}
