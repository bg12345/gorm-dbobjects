package dbobjects

import (
	"fmt"

	"github.com/bg12345/gorm-dbobjects/trigger"
	"gorm.io/gorm"
)

type DBObject interface {
	Kind() string
}

var db *gorm.DB

// Init registers the *gorm.DB that Register applies trigger DDL against.
func Init(conn *gorm.DB) {
	db = conn
}

func Register(objects ...DBObject) error {
	if db == nil {
		return fmt.Errorf("dbobjects: Init must be called before Register")
	}
	d, ok := dialectFor(db.Name())
	if !ok {
		return fmt.Errorf("dbobjects: unsupported dialect %q", db.Name())
	}
	for _, obj := range objects {
		switch o := obj.(type) {
		case trigger.Trigger:
			def, err := o.Build()
			if err != nil {
				return err
			}
			td, ok := d.(triggerDialect)
			if !ok {
				return fmt.Errorf("dbobjects: dialect %q does not support triggers", d.Name())
			}
			sql, err := td.RenderTrigger(def)
			if err != nil {
				return err
			}
			if err := db.Exec(sql).Error; err != nil {
				return fmt.Errorf("dbobjects: applying %s on %q: %w", obj.Kind(), def.Table, err)
			}
		default:
			return fmt.Errorf("dbobjects: unsupported object kind %q", obj.Kind())
		}
	}
	return nil
}
