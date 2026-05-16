package migrations

import (
	"github.com/jmoiron/sqlx"
	"github.com/knadh/koanf/v2"
	"github.com/knadh/stuffbin"
)

func V2_3_0(db *sqlx.DB, fs stuffbin.FileSystem, ko *koanf.Koanf) error {
	// Add 'awaiting_window' to the message_status ENUM so that WhatsApp messages
	// held outside the 24-hour customer service window can be stored safely.
	_, err := db.Exec(`
		DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_enum
				WHERE enumlabel = 'awaiting_window'
				  AND enumtypid = (SELECT oid FROM pg_type WHERE typname = 'message_status')
			) THEN
				ALTER TYPE message_status ADD VALUE 'awaiting_window';
			END IF;
		END$$;
	`)
	return err
}
