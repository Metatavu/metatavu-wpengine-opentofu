// Package openapi holds the fields of the WP Engine API, generated from its
// OpenAPI document, so tests can check that every field the provider sends or
// reads exists.
package openapi

//go:generate go run ./gen

// HasRequestField tells whether the operation's request body may have the
// field (a dotted path).
func HasRequestField(operation, field string) bool {
	return contains(Requests[operation], field)
}

// HasSchemaField tells whether the schema has the field (a dotted path).
func HasSchemaField(schema, field string) bool {
	return contains(Schemas[schema], field)
}

func contains(fields []string, field string) bool {
	for _, f := range fields {
		if f == field {
			return true
		}
	}
	return false
}
