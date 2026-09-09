package procedure_test

import (
	"fmt"

	"github.com/bg12345/gorm-dbobjects/procedure"
)

func Example() {
	proc := procedure.New("recalc_balances").
		Param("user_id", procedure.Int).
		Body("UPDATE accounts SET balance = balance + 1 WHERE id = user_id;")

	def, err := proc.Build()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(def.Name, len(def.Params))
	// Output:
	// recalc_balances 1
}
