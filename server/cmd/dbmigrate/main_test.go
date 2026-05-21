package main

import "testing"

func TestValidateSchemaNameAllowsMigrationShadowAndProdPrefixes(t *testing.T) {
	valid := []string{
		"cats_migration_check",
		"cats_shadow_20260521_090713",
		"cats_prod_20260521_090713",
	}
	for _, schema := range valid {
		if err := validateSchemaName(schema); err != nil {
			t.Fatalf("validateSchemaName(%q) returned error: %v", schema, err)
		}
	}
}

func TestValidateSchemaNameRejectsUnsafeNames(t *testing.T) {
	invalid := []string{
		"",
		"public",
		"pg_catalog",
		"information_schema",
		"openchat",
		"cats-prod-20260521",
		"1cats_prod_20260521",
	}
	for _, schema := range invalid {
		if err := validateSchemaName(schema); err == nil {
			t.Fatalf("validateSchemaName(%q) returned nil error", schema)
		}
	}
}
