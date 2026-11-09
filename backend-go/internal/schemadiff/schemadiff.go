package schemadiff

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"campus-hive/backend-go/internal/database"
)

var ignoredRelations = map[string]bool{
	"schema_migrations":  true,
	"_prisma_migrations": true,
}

type Catalog struct {
	Enums       []string
	Tables      []string
	Columns     []string
	Indexes     []string
	Constraints []string
	Sequences   []string
}

func Load(ctx context.Context, databaseURL string) (Catalog, error) {
	db, err := database.Open(ctx, databaseURL)
	if err != nil {
		return Catalog{}, err
	}
	defer db.Close()
	pool := db.Pool
	result := Catalog{}

	enumRows, err := pool.Query(ctx, `
		SELECT t.typname || ' = ' || string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder)
		FROM pg_type t
		JOIN pg_enum e ON e.enumtypid = t.oid
		JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname = 'public'
		GROUP BY t.typname
		ORDER BY t.typname`)
	if err != nil {
		return Catalog{}, fmt.Errorf("load enums: %w", err)
	}
	result.Enums, err = scanAll(enumRows)
	enumRows.Close()
	if err != nil {
		return Catalog{}, err
	}

	tableRows, err := pool.Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		ORDER BY c.relname`)
	if err != nil {
		return Catalog{}, fmt.Errorf("load tables: %w", err)
	}
	allTables, err := scanAll(tableRows)
	tableRows.Close()
	if err != nil {
		return Catalog{}, err
	}
	for _, name := range allTables {
		if !ignoredRelations[name] {
			result.Tables = append(result.Tables, name)
		}
	}

	columnRows, err := pool.Query(ctx, `
		SELECT table_name || '.' || column_name || ' ' || data_type::text ||
		       coalesce(' nullable=' || is_nullable::text, '') ||
		       coalesce(' default=' || column_default::text, '')
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, column_name`)
	if err != nil {
		return Catalog{}, fmt.Errorf("load columns: %w", err)
	}
	allColumns, err := scanAll(columnRows)
	columnRows.Close()
	if err != nil {
		return Catalog{}, err
	}
	result.Columns = filterByRelation(allColumns)

	indexRows, err := pool.Query(ctx, `
		SELECT c.relname || ' -> ' || pg_get_indexdef(i.indexrelid)
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		ORDER BY c.relname, i.indexrelid`)
	if err != nil {
		return Catalog{}, fmt.Errorf("load indexes: %w", err)
	}
	allIndexes, err := scanAll(indexRows)
	indexRows.Close()
	if err != nil {
		return Catalog{}, err
	}
	result.Indexes = filterByRelation(allIndexes)

	constraintRows, err := pool.Query(ctx, `
		SELECT c.relname || ' -> ' || con.conname || ' ' || con.contype::text || ' ' || pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		ORDER BY c.relname, con.conname`)
	if err != nil {
		return Catalog{}, fmt.Errorf("load constraints: %w", err)
	}
	allConstraints, err := scanAll(constraintRows)
	constraintRows.Close()
	if err != nil {
		return Catalog{}, err
	}
	result.Constraints = filterByRelation(allConstraints)

	sequenceRows, err := pool.Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'S'
		ORDER BY c.relname`)
	if err != nil {
		return Catalog{}, fmt.Errorf("load sequences: %w", err)
	}
	result.Sequences, err = scanAll(sequenceRows)
	sequenceRows.Close()
	if err != nil {
		return Catalog{}, err
	}
	result.Sequences = filterByRelation(result.Sequences)

	return result, nil
}

func Compare(candidate, reference Catalog) []string {
	differences := compare("enums", candidate.Enums, reference.Enums)
	differences = append(differences, compare("tables", candidate.Tables, reference.Tables)...)
	differences = append(differences, compare("columns", candidate.Columns, reference.Columns)...)
	differences = append(differences, compare("indexes", candidate.Indexes, reference.Indexes)...)
	differences = append(differences, compare("constraints", candidate.Constraints, reference.Constraints)...)
	differences = append(differences, compare("sequences", candidate.Sequences, reference.Sequences)...)
	return differences
}

func (c Catalog) Summary() string {
	return fmt.Sprintf("%d enums, %d tables, %d columns, %d indexes, %d constraints",
		len(c.Enums), len(c.Tables), len(c.Columns), len(c.Indexes), len(c.Constraints))
}

func compare(kind string, candidate, reference []string) []string {
	candidateSet := map[string]bool{}
	referenceSet := map[string]bool{}
	for _, value := range candidate {
		candidateSet[value] = true
	}
	for _, value := range reference {
		referenceSet[value] = true
	}
	onlyCandidate := make([]string, 0)
	onlyReference := make([]string, 0)
	for value := range candidateSet {
		if !referenceSet[value] {
			onlyCandidate = append(onlyCandidate, value)
		}
	}
	for value := range referenceSet {
		if !candidateSet[value] {
			onlyReference = append(onlyReference, value)
		}
	}
	sort.Strings(onlyCandidate)
	sort.Strings(onlyReference)
	differences := make([]string, 0, len(onlyCandidate)+len(onlyReference))
	for _, value := range onlyCandidate {
		differences = append(differences, fmt.Sprintf("only in candidate (%s): %s", kind, value))
	}
	for _, value := range onlyReference {
		differences = append(differences, fmt.Sprintf("only in reference (%s): %s", kind, value))
	}
	return differences
}

func filterByRelation(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		relation := value
		if cut := strings.IndexAny(relation, " ."); cut > 0 {
			relation = relation[:cut]
		}
		if ignoredRelations[relation] {
			continue
		}
		out = append(out, value)
	}
	return out
}
