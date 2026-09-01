package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbobjects "github.com/bg12345/gorm-dbobjects"
	"github.com/bg12345/gorm-dbobjects/internal/testutil"
	"github.com/bg12345/gorm-dbobjects/procedure"
)

// --- procedure.Build() unit tests (no DB required) ---

func TestProcedure_Build(t *testing.T) {
	def, err := procedure.New("recalc_balances").
		Param("user_id", procedure.Int).
		Body("UPDATE accounts SET balance = balance + 1 WHERE id = user_id;").
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if def.Name != "recalc_balances" {
		t.Errorf("Name = %q, want %q", def.Name, "recalc_balances")
	}
	if len(def.Params) != 1 || def.Params[0].Name != "user_id" || def.Params[0].Type.Kind != procedure.ParamInt {
		t.Errorf("Params = %+v, want one Int param named user_id", def.Params)
	}
	if def.Body == "" {
		t.Error("Body is empty, want the raw SQL passed to Body()")
	}
}

func TestProcedure_Build_NoName(t *testing.T) {
	_, err := procedure.New("").Body("SELECT 1;").Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error for an empty name")
	}
}

func TestProcedure_Build_NoBody(t *testing.T) {
	_, err := procedure.New("noop").Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error when no Body was set (no Set()-style alternative exists here)")
	}
}

func TestProcedure_Param_DuplicateName_Errors(t *testing.T) {
	_, err := procedure.New("p").
		Param("user_id", procedure.Int).
		Param("user_id", procedure.Text).
		Body("SELECT 1;").
		Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error for a duplicate param name")
	}
}

func TestProcedure_Kind(t *testing.T) {
	if got := procedure.New("p").Kind(); got != "procedure" {
		t.Errorf("Kind() = %q, want %q", got, "procedure")
	}
}

// typeOfModel exists only to exercise TypeOf against every gorm field
// kind, including the ones with no portable equivalent (Uint/Float/
// Bytes) -- none of testutil's real models have a plain int/bool/float
// field to derive from.
type typeOfModel struct {
	ID      uint
	Age     int
	Active  bool
	LastRun time.Time
	Notes   string
	Score   float64
	Payload []byte
}

func TestProcedure_TypeOf(t *testing.T) {
	tests := []struct {
		field string
		want  procedure.ParamKind
	}{
		{"Age", procedure.ParamInt},
		{"Active", procedure.ParamBool},
		{"LastRun", procedure.ParamTime},
		{"Notes", procedure.ParamText},
		{"Score", procedure.ParamFloat},
		{"Payload", procedure.ParamBytes},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			def, err := procedure.New("p").
				Param("x", procedure.TypeOf(&typeOfModel{}, tt.field)).
				Body("SELECT 1;").
				Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if got := def.Params[0].Type.Kind; got != tt.want {
				t.Errorf("TypeOf(%q).Kind = %v, want %v", tt.field, got, tt.want)
			}
		})
	}
}

// TestProcedure_TypeOf_UnsupportedKind_Errors confirms TypeOf's error
// for a field gorm resolves to Uint only surfaces at Build() -- Uint has
// no portable equivalent (Postgres/SQL Server have no native unsigned
// integer type), Raw() is the documented way around it.
func TestProcedure_TypeOf_UnsupportedKind_Errors(t *testing.T) {
	_, err := procedure.New("p").
		Param("x", procedure.TypeOf(&typeOfModel{}, "ID")).
		Body("SELECT 1;").
		Build()
	if err == nil {
		t.Error("Build() error = nil, want error deriving from a Uint field (no portable equivalent)")
	}
}

func TestProcedure_TypeOf_UnknownField_Errors(t *testing.T) {
	_, err := procedure.New("p").
		Param("x", procedure.TypeOf(&typeOfModel{}, "DoesNotExist")).
		Body("SELECT 1;").
		Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error for a field that doesn't exist on the model")
	}
}

// --- client.Register() ---

// TestRegister_Postgres_AppliesProcedure is an integration test: it
// connects to a real Postgres, registers a procedure, actually CALLs it,
// and asserts the real side effect against the live DB -- proving the
// generated DDL isn't just syntactically plausible but genuinely
// executable. Skips itself if no Postgres is reachable.
func TestRegister_Postgres_AppliesProcedure(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}
	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	proc := procedure.New("sp_set_user_name_pg").
		Param("user_id", procedure.Int).
		Param("new_name", procedure.Varchar(100)).
		Body("UPDATE user_master SET name = new_name WHERE id = user_id;")
	if err := client.Register(context.Background(), proc); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), proc) })

	user := testutil.UserMaster{Name: "Ada", Email: fmt.Sprintf("ada-proc-pg-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	if err := db.Exec("CALL sp_set_user_name_pg(?, ?)", user.ID, "Renamed").Error; err != nil {
		t.Fatalf("CALL sp_set_user_name_pg: %v", err)
	}

	var reloaded testutil.UserMaster
	if err := db.First(&reloaded, user.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if reloaded.Name != "Renamed" {
		t.Errorf("Name = %q, want %q (procedure should have run)", reloaded.Name, "Renamed")
	}
}

// TestRegister_MySQL_AppliesProcedure is the MySQL equivalent of
// TestRegister_Postgres_AppliesProcedure. Skips itself if no MySQL is
// reachable.
func TestRegister_MySQL_AppliesProcedure(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}
	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	proc := procedure.New("sp_set_user_name_mysql").
		Param("user_id", procedure.Int).
		Param("new_name", procedure.Varchar(100)).
		Body("UPDATE user_master SET name = new_name WHERE id = user_id;")
	if err := client.Register(context.Background(), proc); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), proc) })

	user := testutil.UserMaster{Name: "Ada", Email: fmt.Sprintf("ada-proc-mysql-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	if err := db.Exec("CALL sp_set_user_name_mysql(?, ?)", user.ID, "Renamed").Error; err != nil {
		t.Fatalf("CALL sp_set_user_name_mysql: %v", err)
	}

	var reloaded testutil.UserMaster
	if err := db.First(&reloaded, user.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if reloaded.Name != "Renamed" {
		t.Errorf("Name = %q, want %q (procedure should have run)", reloaded.Name, "Renamed")
	}
}

// TestRegister_SQLServer_AppliesProcedure is the SQL Server equivalent
// of TestRegister_Postgres_AppliesProcedure. Its Body references
// @user_id/@new_name, not bare names -- SQL Server's own calling
// convention, same portability note trigger Body() already carries.
// Skips itself if no SQL Server is reachable.
func TestRegister_SQLServer_AppliesProcedure(t *testing.T) {
	db, err := testutil.NewSQLServer()
	if err != nil {
		t.Skipf("skipping integration test, no SQL Server reachable: %v", err)
	}
	if err := db.AutoMigrate(&testutil.UserMaster{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	proc := procedure.New("sp_set_user_name_mssql").
		Param("user_id", procedure.Int).
		Param("new_name", procedure.Varchar(100)).
		Body("UPDATE user_master SET name = @new_name WHERE id = @user_id;")
	if err := client.Register(context.Background(), proc); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), proc) })

	user := testutil.UserMaster{Name: "Ada", Email: fmt.Sprintf("ada-proc-mssql-%d@example.com", time.Now().UnixNano())}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { db.Unscoped().Delete(&user) })

	if err := db.Exec("EXEC sp_set_user_name_mssql ?, ?", user.ID, "Renamed").Error; err != nil {
		t.Fatalf("EXEC sp_set_user_name_mssql: %v", err)
	}

	var reloaded testutil.UserMaster
	if err := db.First(&reloaded, user.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if reloaded.Name != "Renamed" {
		t.Errorf("Name = %q, want %q (procedure should have run)", reloaded.Name, "Renamed")
	}
}

// TestRegister_SQLite_RejectsProcedure confirms Register fails, not
// silently no-ops, for a procedure against SQLite -- which has no
// stored procedure concept at all. Cheap (a fresh temp-file DB), so
// unlike the Postgres/MySQL/SQL Server tests above it never skips
// itself, matching every other SQLite test in this package.
func TestRegister_SQLite_RejectsProcedure(t *testing.T) {
	db, err := testutil.NewSQLite(newSQLiteDSN(t))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}

	client := dbobjects.NewClient(db)
	proc := procedure.New("noop").Body("SELECT 1;")
	if err := client.Register(context.Background(), proc); err == nil {
		t.Fatal("Register() error = nil, want error: sqlite has no procedure support")
	}
}
