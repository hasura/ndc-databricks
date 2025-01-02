// cli/main.go

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	_ "github.com/databricks/databricks-sql-go"
	"os"
	"runtime/debug"
)

// ErrorLocation provides context about where an error occurred
type ErrorLocation struct {
	Function string
	Message  string
	Err      error
}

func (e *ErrorLocation) Error() string {
	return fmt.Sprintf("[%s] %s: %v", e.Function, e.Message, e.Err)
}

func debugTableAccess(db *sql.DB) error {
	// Check all accessible catalogs
	catalogQuery := `
    SELECT DISTINCT catalog_name
    FROM information_schema.catalogs
    ORDER BY catalog_name`

	fmt.Println("\nAccessible Catalogs:")
	rows, err := db.Query(catalogQuery)
	if err != nil {
		return fmt.Errorf("failed to query catalogs: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var catalogName string
		if err := rows.Scan(&catalogName); err != nil {
			return fmt.Errorf("failed to scan catalog row: %v", err)
		}
		fmt.Printf("- %s\n", catalogName)
	}

	// Check all accessible schemas
	schemaQuery := `
    SELECT DISTINCT table_catalog, table_schema
    FROM information_schema.tables
    ORDER BY table_catalog, table_schema`

	fmt.Println("\nAccessible Schemas:")
	rows, err = db.Query(schemaQuery)
	if err != nil {
		return fmt.Errorf("failed to query schemas: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var catalog, schema string
		if err := rows.Scan(&catalog, &schema); err != nil {
			return fmt.Errorf("failed to scan schema row: %v", err)
		}
		fmt.Printf("- %s.%s\n", catalog, schema)
	}

	// Check all accessible tables
	tableQuery := `
    SELECT table_catalog, table_schema, table_name, table_type
    FROM information_schema.tables
    ORDER BY table_catalog, table_schema, table_name`

	fmt.Println("\nAccessible Tables:")
	rows, err = db.Query(tableQuery)
	if err != nil {
		return fmt.Errorf("failed to query tables: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var catalog, schema, name, tableType string
		if err := rows.Scan(&catalog, &schema, &name, &tableType); err != nil {
			return fmt.Errorf("failed to scan table row: %v", err)
		}
		fmt.Printf("- %s.%s.%s (%s)\n", catalog, schema, name, tableType)
	}

	return nil
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Panic occurred:\n%v\n", r)
			debug.Stack()
			os.Exit(1)
		}
	}()

	dsn := os.Getenv("DATABRICKS_DSN")
	if dsn == "" {
		panic("No connection string found. Set the DATABRICKS_DSN environment variable.")
	}

	db, err := sql.Open("databricks", dsn)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	err = db.Ping()
	if err != nil {
		panic(err)
	}

	// Add this debug section
	fmt.Println("=== DEBUG INFORMATION ===")
	if err = debugTableAccess(db); err != nil {
		fmt.Printf("Debug error: %v\n", err)
	}
	fmt.Println("=======================")

	catalog := flag.String("catalog", "", "Optional: Specific catalog to introspect")
	schema := flag.String("schema", "", "Optional: Specific schema to introspect (default: default)")
	output := flag.String("output", "", "Optional: Output JSON file path")
	flag.Parse()

	query := buildIntrospectionQuery(*catalog, *schema)
	println("Query: ", query)
	jsonStr, err := executeQuery(db, query)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Pretty print the JSON
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, []byte(jsonStr), "", "  "); err != nil {
		fmt.Printf("Error formatting JSON: %v\n", err)
		os.Exit(1)
	}

	if *output != "" {
		err = os.WriteFile(*output, prettyJSON.Bytes(), 0644)
		if err != nil {
			fmt.Printf("Error writing to file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Results written to %s\n", *output)
	} else {
		fmt.Println(prettyJSON.String())
	}
}

func buildIntrospectionQuery(catalog, schema string) string {
	baseQuery := `
    WITH column_info AS (
        SELECT
            t.table_catalog,
            t.table_schema,
            t.table_name,
            t.table_type,
            map_from_entries(array_agg(
                struct(
                    c.column_name as key,
                    struct(
                        c.column_name as name,
                        UPPER(c.data_type) as scalarType,
                        c.is_nullable = 'YES' as nullable
                    ) as value
                )
            )) as columns,
            null as primary_keys
        FROM information_schema.tables t
        JOIN information_schema.columns c
            ON t.table_catalog = c.table_catalog
            AND t.table_schema = c.table_schema
            AND t.table_name = c.table_name
        WHERE t.table_schema != 'information_schema'
    `

	if catalog != "" {
		baseQuery += fmt.Sprintf("\nAND t.table_catalog = '%s'", catalog)
	}
	if schema != "" {
		print("Schema: ", schema)
		baseQuery += fmt.Sprintf("\nAND t.table_schema = '%s'", schema)
	}

	baseQuery += `
        GROUP BY t.table_catalog, t.table_schema, t.table_name, t.table_type
    )
    SELECT to_json(
        map_from_entries(
            array_agg(
                struct(
                    CONCAT(table_schema, '.', table_name) as key,  -- Include schema in key
                    struct(
                        table_catalog as physicalCatalog,
                        table_schema as physicalSchema,
                        '' as catalog,
                        table_schema as schema,
                        table_name as name,
                        columns as columns,
                        primary_keys as primaryKeys,
                        array() as exportedKeys
                    ) as value
                )
            )
        )
    ) as tables
    FROM column_info`

	return baseQuery
}

// func buildIntrospectionQuery(catalog, schema string) string {
// 	// Default to 'default' schema if none provided
// 	if schema == "" {
// 		schema = "default"
// 	}

// 	baseQuery := `
// 	WITH column_info AS (
// 		SELECT
// 			t.table_catalog,
// 			t.table_schema,
// 			t.table_name,
// 			t.table_type,
// 			map_from_entries(array_agg(
// 				struct(
// 					c.column_name as key,
// 					struct(
// 						c.column_name as name,
// 						UPPER(c.data_type) as scalarType,
// 						c.is_nullable = 'YES' as nullable
// 					) as value
// 				)

// 			)) as columns,
// 			array_remove(collect_list(
// 				CASE
// 					WHEN tc.constraint_type = 'PRIMARY KEY' THEN c.column_name
// 					ELSE NULL
// 				END
// 			), null) as primary_keys
// 		FROM information_schema.tables t
// 		JOIN information_schema.columns c
// 			ON t.table_catalog = c.table_catalog
// 			AND t.table_schema = c.table_schema
// 			AND t.table_name = c.table_name
// 		LEFT JOIN information_schema.table_constraints tc
// 			ON t.table_catalog = tc.table_catalog
// 			AND t.table_schema = tc.table_schema
// 			AND t.table_name = tc.table_name
// 			AND tc.constraint_type = 'PRIMARY KEY'
// 		LEFT JOIN information_schema.key_column_usage kcu
// 			ON tc.constraint_catalog = kcu.constraint_catalog
// 			AND tc.constraint_schema = kcu.constraint_schema
// 			AND tc.constraint_name = kcu.constraint_name
// 			AND c.column_name = kcu.column_name
// 	`

// 	if catalog != "" {
// 		baseQuery += fmt.Sprintf("\nWHERE t.table_catalog = '%s'", catalog)
// 		if schema != "" {
// 			baseQuery += fmt.Sprintf("\nAND t.table_schema = '%s'", schema)
// 		}
// 	} else if schema != "" {
// 		baseQuery += fmt.Sprintf("\nWHERE t.table_schema = '%s'", schema)
// 	}

// 	baseQuery += `
// 		GROUP BY t.table_catalog, t.table_schema, t.table_name, t.table_type
// 	)
// 	SELECT to_json(
// 		map_from_entries(
// 			array_agg(
// 				struct(
// 					table_name as key,
// 					struct(
// 						table_catalog as physicalCatalog,
// 						table_schema as physicalSchema,
// 						'' as catalog,
// 						table_schema as schema,
// 						table_name as name,
// 						columns as columns,
// 						primary_keys as primaryKeys,
// 						array() as exportedKeys
// 					) as value
// 				)

// 			)
// 		)
// 	) as tables
// 	FROM column_info`

// 	return baseQuery
// }

func executeQuery(db *sql.DB, query string) (string, error) {
	const funcName = "executeQuery"
	var jsonStr string

	err := db.QueryRow(query).Scan(&jsonStr)
	if err != nil {
		return "", wrapError(funcName, "failed to execute query", err)
	}

	return jsonStr, nil
}

// wrapError adds function context to errors
func wrapError(function, message string, err error) error {
	return &ErrorLocation{
		Function: function,
		Message:  message,
		Err:      err,
	}
}
