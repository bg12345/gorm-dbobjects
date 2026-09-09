package trigger_test

import (
	"fmt"

	"github.com/bg12345/gorm-dbobjects/trigger"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Name string
}

func Example() {
	tr := trigger.BeforeUpdate(&User{}).
		Set("updated_at", trigger.Now())

	def, err := tr.Build()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(def.Table, def.Timing, def.Event)
	// Output:
	// users BEFORE UPDATE
}
