package schemadiff

import "testing"

func TestCompareDetectsMissingAndExtraObjects(t *testing.T) {
	candidate := Catalog{
		Enums:       []string{"Role = STUDENT"},
		Tables:      []string{"User", "Post"},
		Columns:     []string{"User.id uuid", "Post.id uuid"},
		Indexes:     []string{"Post -> CREATE INDEX Post_slug_key"},
		Constraints: []string{"Post -> Post_pkey p PRIMARY KEY (id)"},
		Sequences:   nil,
	}
	reference := Catalog{
		Enums:       []string{"Role = STUDENT"},
		Tables:      []string{"User", "Post", "Tag"},
		Columns:     []string{"User.id uuid", "Tag.id uuid"},
		Indexes:     nil,
		Constraints: []string{"Post -> Post_pkey p PRIMARY KEY (id)"},
		Sequences:   nil,
	}

	differences := Compare(candidate, reference)
	expected := map[string]bool{
		"only in reference (tables): Tag":                                 true,
		"only in candidate (columns): Post.id uuid":                       true,
		"only in reference (columns): Tag.id uuid":                        true,
		"only in candidate (indexes): Post -> CREATE INDEX Post_slug_key": true,
	}
	if len(differences) != len(expected) {
		t.Fatalf("differences = %v", differences)
	}
	for _, difference := range differences {
		if !expected[difference] {
			t.Errorf("unexpected difference %q", difference)
		}
	}
}

func TestCompareIdenticalCatalogs(t *testing.T) {
	catalog := Catalog{
		Enums:       []string{"Role = STUDENT"},
		Tables:      []string{"User"},
		Columns:     []string{"User.id uuid"},
		Indexes:     []string{"User -> CREATE UNIQUE INDEX User_email_key"},
		Constraints: []string{"User -> User_pkey p PRIMARY KEY (id)"},
	}
	if differences := Compare(catalog, catalog); len(differences) != 0 {
		t.Errorf("differences = %v", differences)
	}
	if summary := catalog.Summary(); summary != "1 enums, 1 tables, 1 columns, 1 indexes, 1 constraints" {
		t.Errorf("summary = %q", summary)
	}
}

func TestIgnoredRelationsAreFiltered(t *testing.T) {
	values := []string{
		"schema_migrations.version bigint nullable=NO",
		"schema_migrations.dirty boolean nullable=NO",
		"_prisma_migrations.id character varying nullable=NO",
		"schema_migrations",
		"schema_migrations -> schema_migrations_pkey p PRIMARY KEY (version)",
		"User.id uuid nullable=NO",
		"User -> User_pkey p PRIMARY KEY (id)",
		"User_email_key",
	}
	filtered := filterByRelation(values)
	expected := []string{
		"User.id uuid nullable=NO",
		"User -> User_pkey p PRIMARY KEY (id)",
		"User_email_key",
	}
	if len(filtered) != len(expected) {
		t.Fatalf("filtered = %v", filtered)
	}
	for i := range expected {
		if filtered[i] != expected[i] {
			t.Errorf("filtered[%d] = %q, want %q", i, filtered[i], expected[i])
		}
	}
}
