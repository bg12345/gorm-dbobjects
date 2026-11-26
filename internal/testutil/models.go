package testutil

import (
	"encoding/json"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type UserMaster struct {
	gorm.Model `json:"-"`
	Name  string `gorm:"type:varchar(100);not null" json:"name"`
	Email string `gorm:"type:varchar(100);unique;not null" json:"email"`
}

func (UserMaster) TableName() string {
	return "user_master"
}

// UserMasterLogs records changes to UserMaster rows. It deliberately has
// no foreign key to user_master, so a row can still be written for a user
// that has just been hard-deleted. IPAddress is nullable because a
// trigger has no way to know it.
type UserMasterLogs struct {
	gorm.Model   `json:"-"`
	UserMasterID uint    `gorm:"not null" json:"user_master_id"`
	IPAddress    string  `gorm:"type:varchar(100)" json:"ip_address"`
	LogMessage   string  `gorm:"type:varchar(100);not null" json:"log_message"`
	OldName      *string `gorm:"type:varchar(100)" json:"old_name"`
	NewName      *string `gorm:"type:varchar(100)" json:"new_name"`
}

func (UserMasterLogs) TableName() string {
	return "user_master_logs"
}

type JSONData struct {
	gorm.Model `json:"-"`
	Data datatypes.JSON `gorm:"type:json;not null" json:"data"`
	Raw json.RawMessage `gorm:"type:json;not null" json:"raw"`
}

func (JSONData) TableName() string {
	return "json_data"
}
