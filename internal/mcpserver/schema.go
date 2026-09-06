package mcpserver

import (
	"encoding/json"
	"fmt"
	"reflect"

	"english-learning-mcp/internal/domain"
	"github.com/google/jsonschema-go/jsonschema"
)

var schemaOptions = &jsonschema.ForOptions{
	TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[domain.CacheState]():     enumSchema("hit", "miss", "refreshed", "stale_fallback"),
		reflect.TypeFor[domain.LearningStatus](): enumSchema("new", "learning", "learned", "archived"),
		reflect.TypeFor[domain.Usefulness]():     enumSchema("low", "normal", "high"),
		reflect.TypeFor[domain.ReviewRating]():   enumSchema("again", "hard", "good", "easy"),
		reflect.TypeFor[SortOrder]():             enumSchema("recent", "oldest", "alphabetical"),
	},
}

func inferredSchema[T any]() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[T](schemaOptions)
	if err != nil {
		return nil, fmt.Errorf("infer JSON schema: %w", err)
	}
	walkSchema(schema, make(map[*jsonschema.Schema]struct{}), func(current *jsonschema.Schema) {
		if current.Items != nil {
			current.Type = "array"
			current.Types = nil
		}
	})
	return schema, nil
}

func enumSchema(values ...string) *jsonschema.Schema {
	enumeration := make([]any, len(values))
	for index, value := range values {
		enumeration[index] = value
	}
	return &jsonschema.Schema{Type: "string", Enum: enumeration}
}

func unionSchema(branches ...*jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", OneOf: branches}
}

func setConst(schema *jsonschema.Schema, property string, value any) {
	propertySchema := schema.Properties[property]
	propertySchema.Const = &value
}

func setDefault(schema *jsonschema.Schema, property string, value string) {
	propertySchema := schema.Properties[property]
	propertySchema.Default = json.RawMessage(value)
}

func setStringBounds(schema *jsonschema.Schema, property string, minimum, maximum int) {
	propertySchema, ok := schema.Properties[property]
	if !ok {
		return
	}
	propertySchema.MinLength = &minimum
	if maximum > 0 {
		propertySchema.MaxLength = &maximum
	}
}

func configureInputSchema(schema *jsonschema.Schema) {
	walkSchema(schema, make(map[*jsonschema.Schema]struct{}), func(current *jsonschema.Schema) {
		if term, ok := current.Properties["term"]; ok {
			minimum := 1
			maximum := 200
			term.MinLength = &minimum
			term.MaxLength = &maximum
		}
		for _, property := range []string{"itemId", "lookupId", "reviewToken"} {
			if identifier, ok := current.Properties[property]; ok {
				minimum := 1
				identifier.MinLength = &minimum
			}
		}
		if reviewToken, ok := current.Properties["reviewToken"]; ok {
			maximum := 200
			reviewToken.MaxLength = &maximum
		}
		if comment, ok := current.Properties["comment"]; ok {
			maximum := 1000
			comment.MaxLength = &maximum
		}
		if confidence, ok := current.Properties["confidence"]; ok {
			minimum := 0.0
			maximum := 1.0
			confidence.Minimum = &minimum
			confidence.Maximum = &maximum
		}
		if description, ok := current.Properties["customDescription"]; ok {
			maximum := 5000
			description.MaxLength = &maximum
		}
		if title, ok := current.Properties["title"]; ok {
			maximum := 200
			title.MaxLength = &maximum
		}
		if sourceURL, ok := current.Properties["url"]; ok {
			maximum := 2000
			sourceURL.MaxLength = &maximum
		}
		for _, property := range []string{"tags", "notes", "examples"} {
			if values, ok := current.Properties[property]; ok {
				maximumItems := 100
				if property == "tags" {
					maximumItems = 50
				}
				values.MaxItems = &maximumItems
			}
		}
	})
}

func configureListSchema(schema *jsonschema.Schema) {
	if limit, ok := schema.Properties["limit"]; ok {
		minimum := 1.0
		maximum := 100.0
		limit.Minimum = &minimum
		limit.Maximum = &maximum
		limit.Default = json.RawMessage("50")
	}
	if _, ok := schema.Properties["sort"]; ok {
		setDefault(schema, "sort", `"recent"`)
	}
}

func walkSchema(
	schema *jsonschema.Schema,
	visited map[*jsonschema.Schema]struct{},
	visit func(*jsonschema.Schema),
) {
	if schema == nil {
		return
	}
	if _, ok := visited[schema]; ok {
		return
	}
	visited[schema] = struct{}{}
	visit(schema)
	for _, property := range schema.Properties {
		walkSchema(property, visited, visit)
	}
	walkSchema(schema.Items, visited, visit)
	for _, branch := range schema.OneOf {
		walkSchema(branch, visited, visit)
	}
	for _, branch := range schema.AnyOf {
		walkSchema(branch, visited, visit)
	}
	for _, branch := range schema.AllOf {
		walkSchema(branch, visited, visit)
	}
	for _, definition := range schema.Defs {
		walkSchema(definition, visited, visit)
	}
}
