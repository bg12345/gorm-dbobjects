package view_test

import (
	"testing"

	"github.com/bg12345/gorm-dbobjects/view"
)

func TestBuild_Materialized(t *testing.T) {
	plain, err := view.New("v").Raw("SELECT 1").Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if plain.Materialized {
		t.Error("Materialized = true on a view that never called Materialized(), want false")
	}

	mat, err := view.New("v").Raw("SELECT 1").Materialized().Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !mat.Materialized {
		t.Error("Materialized = false after Materialized(), want true")
	}
}
