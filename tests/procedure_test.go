package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	dbobjects "github.com/bg12345/gorm-dbobjects"
	"github.com/bg12345/gorm-dbobjects/internal/testutil"
	"github.com/bg12345/gorm-dbobjects/procedure"
	"gorm.io/datatypes"
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
// kind, including the ones with no portable equivalent (Uint) -- none
// of testutil's real models have a plain int/bool/float field to
// derive from.
type typeOfModel struct {
	ID      uint
	Age     int
	Active  bool
	LastRun time.Time
	Notes   string
	Score   float64
	Payload []byte
	Data    datatypes.JSON
	Raw     json.RawMessage
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
		{"Data", procedure.ParamJSON},
		{"Raw", procedure.ParamJSON},
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

// assertJSONStatusOK unmarshals raw and checks its "status" field is
// "ok" -- shared by the three AppliesProcedure_JSON tests' data/raw
// assertions, both of which repeat this exact check per engine.
func assertJSONStatusOK(t *testing.T, label string, raw []byte) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal %s %q: %v", label, raw, err)
	}
	if got["status"] != "ok" {
		t.Errorf(`%s["status"] = %v, want "ok" (procedure should have written the JSON param)`, label, got["status"])
	}
}

// TestRegister_Postgres_AppliesProcedure_JSON is an integration test
// verifying a procedure.JSON param actually round-trips through both a
// real JSONB column (Data, datatypes.JSON) and a plain NVARCHAR-style
// column decoded as json.RawMessage (Raw) -- not just that the DDL
// renders, that Postgres accepts a JSON-text bind value for a
// jsonb-typed procedure parameter and stores it correctly, and that
// both of TypeOf's two JSON-recognized Go types read back correctly.
// Skips itself if no Postgres is reachable.
func TestRegister_Postgres_AppliesProcedure_JSON(t *testing.T) {
	db, err := testutil.NewPostgres()
	if err != nil {
		t.Skipf("skipping integration test, no Postgres reachable: %v", err)
	}
	if err := db.AutoMigrate(&testutil.JSONData{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	proc := procedure.New("sp_insert_json_pg").
		Param("payload", procedure.JSON).
		Body("INSERT INTO json_data (data, raw) VALUES (payload, payload);")
	if err := client.Register(context.Background(), proc); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), proc) })

	if err := db.Exec("CALL sp_insert_json_pg(?)", `{"status":"ok","count":3}`).Error; err != nil {
		t.Fatalf("CALL sp_insert_json_pg: %v", err)
	}

	var row testutil.JSONData
	if err := db.Select("data", "raw").Last(&row).Error; err != nil {
		t.Fatalf("querying row: %v", err)
	}
	assertJSONStatusOK(t, "Data", []byte(row.Data))
	assertJSONStatusOK(t, "Raw", []byte(row.Raw))
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

// TestRegister_MySQL_AppliesProcedure_JSON is the MySQL equivalent of
// TestRegister_Postgres_AppliesProcedure_JSON, checking both Data and
// Raw the same way. Skips itself if no MySQL is reachable.
func TestRegister_MySQL_AppliesProcedure_JSON(t *testing.T) {
	db, err := testutil.NewMySQL()
	if err != nil {
		t.Skipf("skipping integration test, no MySQL reachable: %v", err)
	}
	if err := db.AutoMigrate(&testutil.JSONData{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	client := dbobjects.NewClient(db)
	proc := procedure.New("sp_insert_json_mysql").
		Param("payload", procedure.JSON).
		Body("INSERT INTO json_data (data, raw) VALUES (payload, payload);")
	if err := client.Register(context.Background(), proc); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), proc) })

	if err := db.Exec("CALL sp_insert_json_mysql(?)", `{"status":"ok","count":3}`).Error; err != nil {
		t.Fatalf("CALL sp_insert_json_mysql: %v", err)
	}

	var row testutil.JSONData
	if err := db.Select("data", "raw").Last(&row).Error; err != nil {
		t.Fatalf("querying row: %v", err)
	}
	assertJSONStatusOK(t, "Data", []byte(row.Data))
	assertJSONStatusOK(t, "Raw", []byte(row.Raw))
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

// TestRegister_SQLServer_AppliesProcedure_JSON is the SQL Server
// equivalent of TestRegister_Postgres_AppliesProcedure_JSON, with two
// real differences (both driver/library gaps, not this package's own):
//   - It can't use AutoMigrate(&testutil.JSONData{}) the way
//     Postgres/MySQL do. gorm.io/datatypes.JSON's own GormDBDataType
//     (what AutoMigrate consults for the column type) only has cases
//     for sqlite/mysql/postgres -- for sqlserver it returns "", so gorm
//     falls back to the field's raw DataType string ("json", set
//     unconditionally by JSON's GormDataType() method) verbatim as the
//     column type, and T-SQL has no json type keyword at all. Creating
//     the table manually with the same name/columns sidesteps that gap
//     while still letting testutil.JSONData be used to read the row
//     back afterward.
//   - Every Select() below is restricted to just the data column: the
//     SQL Server driver returns an NVARCHAR value as a plain Go string,
//     and json.RawMessage (testutil.JSONData's Raw field) has no
//     sql.Scanner implementation to accept that -- scanning the full
//     row fails with "unsupported Scan ... into type *json.RawMessage"
//     on this engine specifically (Postgres/MySQL's drivers return
//     []byte for their JSON/JSONB columns instead, which does scan into
//     json.RawMessage without issue).
//
// Skips itself if no SQL Server is reachable.
func TestRegister_SQLServer_AppliesProcedure_JSON(t *testing.T) {
	db, err := testutil.NewSQLServer()
	if err != nil {
		t.Skipf("skipping integration test, no SQL Server reachable: %v", err)
	}
	if err := db.Exec(`IF NOT EXISTS (SELECT * FROM sys.tables WHERE name = 'json_data')
		CREATE TABLE json_data (
			id INT IDENTITY(1,1) PRIMARY KEY,
			data NVARCHAR(MAX) NOT NULL,
			raw NVARCHAR(MAX) NOT NULL,
			created_at DATETIMEOFFSET,
			updated_at DATETIMEOFFSET,
			deleted_at DATETIMEOFFSET
		)`).Error; err != nil {
		t.Fatalf("creating json_data table: %v", err)
	}

	client := dbobjects.NewClient(db)
	proc := procedure.New("sp_insert_json_mssql").
		Param("payload", procedure.JSON).
		Body("INSERT INTO json_data (data, raw) VALUES (@payload, @payload);")
	if err := client.Register(context.Background(), proc); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() { _ = client.Drop(context.Background(), proc) })

	if err := db.Exec("EXEC sp_insert_json_mssql ?", `{"status":"ok","count":3}`).Error; err != nil {
		t.Fatalf("EXEC sp_insert_json_mssql: %v", err)
	}

	var row testutil.JSONData
	if err := db.Select("data").Last(&row).Error; err != nil {
		t.Fatalf("querying row: %v", err)
	}
	assertJSONStatusOK(t, "Data", []byte(row.Data))

	// The above only proves the stored text matches what was written,
	// from Go's side. Since NVARCHAR(MAX) is just an opaque string
	// column to SQL Server, that alone doesn't prove it's actually
	// valid, queryable JSON there -- ISJSON/JSON_VALUE (Microsoft's own
	// native JSON functions, the ones procedure.JSON's doc comment
	// names) do, so this drives the query through them instead of only
	// checking content in Go.
	var isJSON int
	if err := db.Model(&testutil.JSONData{}).Select("ISJSON(data)").Order("id DESC").Limit(1).Row().Scan(&isJSON); err != nil {
		t.Fatalf("querying ISJSON(data): %v", err)
	}
	if isJSON != 1 {
		t.Errorf("ISJSON(data) = %d, want 1 (stored value should be valid JSON, queryable via SQL Server's native JSON functions)", isJSON)
	}
	var status string
	if err := db.Model(&testutil.JSONData{}).Select("JSON_VALUE(data, '$.status')").Order("id DESC").Limit(1).Row().Scan(&status); err != nil {
		t.Fatalf("querying JSON_VALUE(data, '$.status'): %v", err)
	}
	if status != "ok" {
		t.Errorf("JSON_VALUE(data, '$.status') = %q, want %q", status, "ok")
	}

	// raw can't be scanned into testutil.JSONData.Raw here (see the
	// doc comment above) -- scan it into a string instead, which the
	// driver's NVARCHAR value converts into cleanly, then convert that
	// to []byte for the same assertion helper.
	var raw string
	if err := db.Model(&testutil.JSONData{}).Select("raw").Order("id DESC").Limit(1).Row().Scan(&raw); err != nil {
		t.Fatalf("querying raw column: %v", err)
	}
	assertJSONStatusOK(t, "Raw", []byte(raw))
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
