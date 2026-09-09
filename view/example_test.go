package view_test

import (
	"fmt"

	"github.com/bg12345/gorm-dbobjects/view"
)

func Example() {
	v := view.New("active_users").
		Raw("SELECT * FROM users WHERE active = true")

	def, err := v.Build()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(def.Name)
	// Output:
	// active_users
}
