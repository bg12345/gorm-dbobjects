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

func ExampleView_Materialized() {
	def, err := view.New("daily_totals").
		Raw("SELECT count(*) FROM users").
		Materialized().
		Build()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(def.Name, def.Materialized)
	// Output:
	// daily_totals true
}
