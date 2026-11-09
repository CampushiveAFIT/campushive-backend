package schemadiff

import (
	"github.com/jackc/pgx/v5"
)

func scanAll(rows pgx.Rows) ([]string, error) {
	defer rows.Close()
	values := make([]string, 0, 16)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}
