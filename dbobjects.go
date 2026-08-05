package dbobjects

import (
	"fmt"

	"github.com/bg12345/gorm-dbobjects/trigger"
	"gorm.io/gorm"
)

var db *gorm.DB

// Init registers the *gorm.DB that Register applies trigger DDL against.
func Init(conn *gorm.DB) {
	db = conn
}

// Register builds each trigger's definition and executes the resulting
// DDL against the DB configured via Init, using the dialect that matches
// that DB's driver (see dialect.go).
func Register(triggers ...trigger.Trigger) error {
	if db == nil {
		return fmt.Errorf("dbobjects: Init must be called before Register")
	}
	d, ok := dialectFor(db.Name())
	if !ok {
		return fmt.Errorf("dbobjects: unsupported dialect %q", db.Name())
	}
	for _, t := range triggers {
		def, err := t.Build()
		if err != nil {
			return err
		}
		if err := db.Exec(d.render(def)).Error; err != nil {
			return fmt.Errorf("dbobjects: applying trigger on %q: %w", def.Table, err)
		}
	}
	return nil
}
