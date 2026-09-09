package dbobjects_test

import (
	"context"
	"log"

	dbobjects "github.com/bg12345/gorm-dbobjects"
	"github.com/bg12345/gorm-dbobjects/trigger"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Name string
}

// Example needs a real, live connection (Register applies DDL, Render
// resolves the connected dialect), so it's compiled but never executed
// -- there's no // Output: comment for go test to run against.
func Example() {
	db, err := gorm.Open(postgres.Open("your-dsn"), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	// Wrap your *gorm.DB once, wherever you already set it up.
	client := dbobjects.NewClient(db)

	tr := trigger.BeforeUpdate(&User{}).
		Set("updated_at", trigger.Now())

	if err := client.Register(context.Background(), tr); err != nil {
		log.Fatal(err)
	}
}
